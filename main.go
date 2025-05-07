// main.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	"github.com/redis/go-redis/v9"
)

type RawEvent struct {
	Type string `json:"type"`
	ID   string `json:"ID"`
	NA   string `json:"NA"`
	L1   string `json:"L1"`
	L2   string `json:"L2"`
	L3   string `json:"L3"`
	SS   string `json:"SS"`
}

type Participant struct {
	Type string `json:"type"`
	ID   string `json:"ID"`
	NA   string `json:"NA"`
	OD   string `json:"OD"`
}

type Match struct {
	MatchName string            `json:"match"`
	Time      string            `json:"time"`
	Odds      map[string]string `json:"odds"`
}

type APIResponse struct {
	Results [][]json.RawMessage `json:"results"`
}

const (
	apiURL   = "https://bookiesapi.com/api/get.php?login=Kaerdin&token=36365-oW3gmNHXB4SWy2S&task=bet365live"
	redisKey = "matches"
	cacheTTL = 5 * time.Minute
)

var (
	ctx         = context.Background()
	redisClient *redis.Client
)

func initRedis() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	redisClient = redis.NewClient(&redis.Options{
		Addr:         addr,
		DB:           0,
		PoolSize:     20,
		MinIdleConns: 5,
		PoolTimeout:  30 * time.Second,
	})

	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("❌ Ошибка подключения к Redis: %v", err)
	}
	log.Println("✅ Успешное подключение к Redis")
}

func fetchAndBuild() map[string]map[string]map[string][]Match {
	client := resty.New().
		SetTimeout(10 * time.Second).
		SetRetryCount(3).
		SetRetryWaitTime(2 * time.Second).
		SetRetryMaxWaitTime(10 * time.Second)

	resp, err := client.R().
		SetHeader("Accept", "application/json").
		Get(apiURL)

	if err != nil {
		log.Printf("❌ Ошибка запроса к API: %v", err)
		return nil
	}

	if resp.StatusCode() != http.StatusOK {
		log.Printf("❌ Ошибка API: статус %d", resp.StatusCode())
		return nil
	}

	var apiResp APIResponse
	if err := json.Unmarshal(resp.Body(), &apiResp); err != nil {
		log.Printf("❌ Ошибка парсинга JSON: %v", err)
		return nil
	}

	var events []RawEvent
	var participants []Participant

	for _, block := range apiResp.Results {
		for _, item := range block {
			var preview struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(item, &preview); err != nil {
				continue
			}
			switch preview.Type {
			case "EV":
				var ev RawEvent
				if err := json.Unmarshal(item, &ev); err == nil {
					events = append(events, ev)
				}
			case "PA":
				var pa Participant
				if err := json.Unmarshal(item, &pa); err == nil {
					participants = append(participants, pa)
				}
			}
		}
	}

	result := make(map[string]map[string]map[string][]Match)

	for _, ev := range events {
		odds := make(map[string]string)

		for _, pa := range participants {
			if strings.HasPrefix(pa.ID, ev.ID) {
				odds[pa.NA] = pa.OD
			}
		}

		sport := ev.L1
		country := ev.L3
		tournament := ev.L2

		if result[sport] == nil {
			result[sport] = make(map[string]map[string][]Match)
		}
		if result[sport][country] == nil {
			result[sport][country] = make(map[string][]Match)
		}

		match := Match{
			MatchName: ev.NA,
			Time:      ev.SS,
			Odds:      odds,
		}

		result[sport][country][tournament] = append(result[sport][country][tournament], match)
	}

	return result
}

func saveToRedis(key string, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Println("❌ Ошибка сериализации данных:", err)
		return
	}

	err = redisClient.Set(ctx, key, jsonData, cacheTTL).Err()
	if err != nil {
		log.Println("❌ Ошибка записи в Redis:", err)
	}
}

func loadFromRedis(key string, target interface{}) error {
	val, err := redisClient.Get(ctx, key).Result()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(val), target)
}

func main() {
	initRedis()

	go func() {
		for {
			data := fetchAndBuild()
			if data != nil {
				saveToRedis(redisKey, data)
				log.Println("✅ Данные обновлены и сохранены в Redis")
			}
			time.Sleep(2 * time.Minute)
		}
	}()

	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://127.0.0.1:5173", "http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type"},
		AllowCredentials: true,
	}))

	r.GET("/structured", func(c *gin.Context) {
		var result map[string]map[string]map[string][]Match
		err := loadFromRedis(redisKey, &result)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка загрузки из Redis"})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.Run(":8081")
}
