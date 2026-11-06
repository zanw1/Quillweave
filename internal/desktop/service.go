package desktop

import (
	"encoding/json"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"os"
	"path/filepath"
	"quillweave/internal/core"
)

type Service struct {
	engine *core.Engine
	app    *application.App
}

// ExportArchiveTransfer copies a reserved backup through the native save dialog without large IPC content.
func (service *Service) ExportArchiveTransfer(transferID, filename string) (bool, error) {
	path, err := service.app.Dialog.SaveFile().SetFilename(filename).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil || path == "" {
		return false, err
	}
	if filepath.Ext(path) != ".json" {
		return false, errors.New("Backup files must use .json")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".quillweave-backup-*")
	if err != nil {
		return false, err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := service.engine.WriteArchiveTransfer(transferID, file); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporary, path); err != nil {
		return false, err
	}
	return true, nil
}

// NewService connects native windows to the shared domain service.
func NewService(engine *core.Engine) *Service { return &Service{engine: engine} }

// Attach attaches native dialogs before the window starts.
func Attach(service *Service, app *application.App) { service.app = app }

// Dispatch transports existing command names and arguments through JSON.
func (service *Service) Dispatch(command, argsJSON string) (any, error) {
	return service.engine.Dispatch(command, json.RawMessage(argsJSON))
}

// DispatchEnvelope gives the new search client the same stable envelope as loopback HTTP.
func (service *Service) DispatchEnvelope(command, argsJSON string) core.CommandEnvelope {
	if !core.IsSearchCommand(command) {
		return core.CommandEnvelope{Error: "Unknown search command", ErrorCode: "SEARCH_INVALID_ARGUMENT"}
	}
	value, err := service.engine.Dispatch(command, json.RawMessage(argsJSON))
	return core.Envelope(value, err)
}

// PickDirectory opens a native folder picker and returns an empty value on cancellation.
func (service *Service) PickDirectory() (string, error) {
	return service.PickDirectoryWithLanguage("en")
}

// PickDirectoryWithLanguage localizes the native picker title for the interface preference.
func (service *Service) PickDirectoryWithLanguage(language string) (string, error) {
	title := "Select legacy project folder"
	if language == "zh-CN" {
		title = "选择旧作品文件夹"
	}
	return service.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
}

// Export writes Markdown or JSON through the author's native save dialog.
func (service *Service) Export(filename, content string) error {
	path, err := service.app.Dialog.SaveFile().SetFilename(filename).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	extension := filepath.Ext(path)
	if extension != ".md" && extension != ".json" {
		return errors.New("Export files must use .md or .json")
	}
	return os.WriteFile(path, []byte(content), 0600)
}
