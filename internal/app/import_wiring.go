package app

import (
	"path/filepath"

	"athena/internal/importer"
	"athena/internal/repository"
	ucimport "athena/internal/usecase/import"
)

// WireImportDependencies initializes the import-related dependencies
func (app *Application) WireImportDependencies(deps *Dependencies) {
	// Initialize import repository
	deps.ImportRepo = repository.NewImportRepository(app.DB)

	// Initialize yt-dlp wrapper
	importDir := filepath.Join(app.Config.StorageDir, "imports")
	ytdlp := importer.NewYtDlp("yt-dlp", importDir)

	// Initialize import service
	deps.ImportService = ucimport.NewService(
		deps.ImportRepo,
		deps.VideoRepo,
		deps.EncodingRepo,
		ytdlp,
		app.Config,
		app.Config.StorageDir,
	)
}
