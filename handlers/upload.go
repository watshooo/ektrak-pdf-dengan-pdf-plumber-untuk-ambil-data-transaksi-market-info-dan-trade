package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"pdfEkstrak/config"
	"pdfEkstrak/database"
	"pdfEkstrak/services"

	"github.com/gofiber/fiber/v2"
)

// UploadHandler menangani upload PDF files (single atau multiple)
type UploadHandler struct {
	db            *database.DB
	cfg           *config.AppConfig
	extractionSvc *services.ExtractionService
}

// NewUploadHandler membuat instance baru dari UploadHandler
func NewUploadHandler(
	db *database.DB,
	cfg *config.AppConfig,
	extractionSvc *services.ExtractionService,
) *UploadHandler {
	return &UploadHandler{
		db:            db,
		cfg:           cfg,
		extractionSvc: extractionSvc,
	}
}

// Handle menangani HTTP request untuk upload PDF files
// Supports: Single file atau Multiple files (1-5 files)
// Field name: "files" (plural, untuk support multiple)
func (h *UploadHandler) Handle(c *fiber.Ctx) error {
	log.Println("📥 [Upload] Request received")

	// Parse multipart form dengan semua files
	// CRITICAL: Menggunakan MultipartForm() untuk support multiple files
	form, err := c.MultipartForm()
	if err != nil {
		log.Printf("❌ [Upload] Form parsing error: %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   fmt.Sprintf("Form parsing error: %v", err),
		})
	}

	// Ambil semua files dengan field name "files"
	// IMPORTANT: Field name harus "files" (plural)
	files := form.File["files"]

	// Validation: At least 1 file
	if len(files) == 0 {
		log.Println("❌ [Upload] No files provided")
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   "No files provided. Select at least one PDF.",
		})
	}

	// Validation: Max 5 files per batch
	if len(files) > 5 {
		log.Printf("❌ [Upload] Too many files: %d (max 5)", len(files))
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   "Maximum 5 files allowed per batch",
		})
	}

	log.Printf("✓ [Upload] Received %d file(s) untuk processing", len(files))

	// Ensure upload directory exists
	if err := os.MkdirAll(h.cfg.UploadDir, 0755); err != nil {
		log.Printf("❌ [Upload] Failed to create upload dir: %v", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"success": false,
			"error":   "Failed to create upload directory",
		})
	}

	// Process setiap file
	var uploadedFiles []string
	var jobIDs []string
	var failedFile string

	for idx, file := range files {
		log.Printf("   [File %d/%d] Processing: %s (%d bytes)",
			idx+1, len(files), file.Filename, file.Size)

		// Validation: File must be PDF
		contentType := file.Header.Get("Content-Type")
		if contentType != "application/pdf" {
			log.Printf("❌ [Upload] Invalid file type: %s (got %s)",
				file.Filename, contentType)
			failedFile = fmt.Sprintf("Invalid file: %s. Only PDF allowed.", file.Filename)
			break
		}

		// Validation: File size max 50MB
		const maxFileSize int64 = 50 * 1024 * 1024
		if file.Size > maxFileSize {
			log.Printf("❌ [Upload] File too large: %s (%dMB)",
				file.Filename, file.Size/(1024*1024))
			failedFile = fmt.Sprintf("File too large: %s. Max 50MB.", file.Filename)
			break
		}

		// Validation: File not empty
		if file.Size == 0 {
			log.Printf("❌ [Upload] File is empty: %s", file.Filename)
			failedFile = fmt.Sprintf("File is empty: %s", file.Filename)
			break
		}

		// Create job record di database
		jobID, err := h.db.CreateJob(file.Filename, file.Size)
		if err != nil {
			log.Printf("❌ [Upload] Failed to create job: %v", err)
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
				"success": false,
				"error":   "Failed to create extraction job",
			})
		}
		log.Printf("   ✓ [Job] Job created: %s", jobID)

		// Build unique file path dengan jobID untuk prevent collision
		uploadPath := filepath.Join(
			h.cfg.UploadDir,
			fmt.Sprintf("%s_%s", jobID, file.Filename),
		)

		// Save file to disk
		if err := c.SaveFile(file, uploadPath); err != nil {
			log.Printf("❌ [Upload] Failed to save file: %v", err)
			h.db.UpdateJobStatus(jobID, "FAILED", "Failed to save file to disk")
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
				"success": false,
				"error":   fmt.Sprintf("Failed to save file: %s", file.Filename),
			})
		}
		log.Printf("   ✓ [File] Saved to: %s", uploadPath)

		// Trigger extraction sebagai background goroutine (async)
		// IMPORTANT: Ini non-blocking, return response immediately
		log.Printf("   [Job %s] 🚀 Triggering extraction", jobID)
		go h.extractionSvc.ExtractFromPDF(jobID, uploadPath)

		uploadedFiles = append(uploadedFiles, file.Filename)
		jobIDs = append(jobIDs, jobID)
	}

	// Handle jika ada file yang gagal validation
	if failedFile != "" {
		log.Printf("❌ [Upload] Validation failed: %s", failedFile)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   failedFile,
		})
	}

	// Build response dengan semua job info
	jobs := make([]fiber.Map, 0, len(jobIDs))
	for i, jobID := range jobIDs {
		jobs = append(jobs, fiber.Map{
			"job_id":    jobID,
			"file_name": uploadedFiles[i],
			"status":    "PENDING",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	}

	log.Printf("✅ [Upload] Successfully created %d job(s)", len(jobs))

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"success":         true,
		"files_processed": len(uploadedFiles),
		"files":           uploadedFiles,
		"jobs":            jobs,
	})
}
