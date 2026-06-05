package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trendyol-api-service/config"
	"trendyol-api-service/handlers"
	"trendyol-api-service/service"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	// NewTrendyolService returns immediately; browser init runs in background.
	svc := service.NewTrendyolService(cfg)
	h := handlers.New(svc)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())

	r.GET("/health", h.Health)

	v1 := r.Group("/api/v1")
	v1.Use(requestTimeout(cfg.TotalTimeout))
	{
		v1.GET("/product/:id", h.GetProduct)
		v1.POST("/products", h.GetProductBatch)
		v1.GET("/products", h.GetProductsQuery)
	}

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: cfg.TotalTimeout + 5*time.Second, // slightly above TotalTimeout
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Trendyol API Service on :%s (total timeout: %s)", cfg.Port, cfg.TotalTimeout)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("forced shutdown: %v", err)
	}
	log.Println("Done.")
}

// requestTimeout wraps each request context with a hard deadline.
// Ensures the client always gets a response — even if all strategies are slow.
func requestTimeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("%s %s → %d (%s)",
			c.Request.Method,
			c.Request.URL.Path,
			c.Writer.Status(),
			time.Since(start).Round(time.Millisecond),
		)
	}
}
