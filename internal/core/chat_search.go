package core

import "errors"

// validateRequestedSearch makes an explicit unavailable service selection visible before accepting a chat request.
func (engine *Engine) validateRequestedSearch(request AIRequest, modelID string) error {
	if request.SearchSelection == nil || request.SearchSelection.Mode != "profile" {
		return nil
	}
	if request.SearchSelection.ProfileID == "" {
		return errors.New("Choose an available web search service")
	}
	return engine.store.withReadSnapshot(func(db documentDB) error {
		_, _, err := resolveSearchPlan(db, searchStart{ProjectID: request.ProjectID, SessionID: request.SessionID, ModelProfileID: modelID, Selection: *request.SearchSelection, Purpose: "research", Operation: "search"})
		return err
	})
}
