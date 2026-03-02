package main

import (
	"log"
	"net/http"

	"pdfEkstrak/config"
	"pdfEkstrak/database"
	"pdfEkstrak/handlers"
	"pdfEkstrak/services"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	log.Println("🚀 Starting PDF Extractor Server")

	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Config loading failed: %v", err)
	}

	log.Printf("   Port: %s | Environment: %s", cfg.Port, cfg.Environment)

	// Initialize database
	db, err := database.Initialize(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("❌ Database initialization failed: %v", err)
	}
	log.Println("✓ Database initialized")
	defer db.Close()

	// Initialize services
	extractionSvc := services.NewExtractionService(db, cfg)

	// Initialize handlers
	uploadHandler := handlers.NewUploadHandler(db, cfg, extractionSvc)
	resultsHandler := handlers.NewResultsHandler(db)

	// Setup Fiber app
	app := fiber.New(fiber.Config{
		AppName: "PDF Extractor",
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())

	// Serve static files
	app.Static("/", "./templates")

	// API Routes
	api := app.Group("/api")

	// Serve static files dengan cache control
	app.Static("/", "./templates", fiber.Static{
		Compress:      true,
		ByteRange:     true,
		Browse:        false,
		CacheDuration: 0, // No cache - always fetch fresh
		MaxAge:        0,
	})

	// Add cache control headers untuk API
	api.Use(func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		return c.Next()
	})

	// Health check
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"status":  "ok",
			"service": "pdf-extractor",
		})
	})

	// Upload endpoint
	api.Post("/upload", uploadHandler.Handle)

	// Results endpoints
	api.Get("/results/:jobID", resultsHandler.GetJob)
	api.Get("/results/:jobID/status", resultsHandler.GetStatus)
	api.Get("/results", resultsHandler.ListJobs)

	// Start server
	log.Printf("✅ Server running on :%s", cfg.Port)
	log.Printf("   Web: http://localhost:%s", cfg.Port)
	log.Printf("   API: http://localhost:%s/api/health", cfg.Port)

	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("❌ Server error: %v", err)
	}
}
