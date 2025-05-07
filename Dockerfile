# Start from the official Go image
FROM golang:1.24-alpine

# Set working directory
WORKDIR /app

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app
RUN go build -o myapp

# Expose port
EXPOSE 8080

# Start the app
CMD ["./myapp"]
