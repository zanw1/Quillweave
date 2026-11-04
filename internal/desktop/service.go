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

// NewService connects native windows to the shared domain service.
func NewService(engine *core.Engine) *Service { return &Service{engine: engine} }

// Attach attaches native dialogs before the window starts.
func Attach(service *Service, app *application.App) { service.app = app }

// Dispatch transports existing command names and arguments through JSON.
func (service *Service) Dispatch(command, argsJSON string) (any, error) {
	return service.engine.Dispatch(command, json.RawMessage(argsJSON))
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
