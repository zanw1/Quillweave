package core

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
)

// WorldLocation represents an author-reviewed place or region and its narrative references.
type WorldLocation struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	RegionID    string   `json:"region_id"`
	Description string   `json:"description"`
	X           float64  `json:"x"`
	Y           float64  `json:"y"`
	EntityIDs   []string `json:"entity_ids"`
	PlanIDs     []string `json:"plan_ids"`
	ChapterIDs  []string `json:"chapter_ids"`
}

// WorldRoute stores a directed or bidirectional journey with an explicit travel-time constraint.
type WorldRoute struct {
	ID            string  `json:"id"`
	FromID        string  `json:"from_id"`
	ToID          string  `json:"to_id"`
	Transport     string  `json:"transport"`
	TravelHours   float64 `json:"travel_hours"`
	Bidirectional bool    `json:"bidirectional"`
	Description   string  `json:"description"`
}

// mapChangeImpacts follows explicit location and route links to the plans and chapters needing review.
func mapChangeImpacts(before, after Project, operations []ChangeOperation) []ChangeImpact {
	locations, routes := map[string]bool{}, map[string]bool{}
	for _, operation := range operations {
		if operation.TargetKind == TargetLocation {
			locations[operation.TargetID] = true
		}
		if operation.TargetKind == TargetRoute {
			routes[operation.TargetID] = true
		}
	}
	if len(locations)+len(routes) == 0 {
		return nil
	}
	for _, project := range []Project{before, after} {
		for _, route := range project.Studio.Routes {
			if routes[route.ID] {
				locations[route.FromID], locations[route.ToID] = true, true
			}
		}
	}
	plans, chapters := map[string]bool{}, map[string]bool{}
	for _, project := range []Project{before, after} {
		for _, location := range project.Studio.Locations {
			if !locations[location.ID] {
				continue
			}
			for _, id := range location.PlanIDs {
				plans[id] = true
			}
			for _, id := range location.ChapterIDs {
				chapters[id] = true
			}
		}
	}
	result := []ChangeImpact{}
	for _, plan := range after.Studio.Plans {
		if !plans[plan.ID] {
			continue
		}
		result = append(result, ChangeImpact{Kind: "plan", ID: plan.ID, Title: plan.Title})
		for _, chapter := range after.Chapters {
			if plan.StartChapter > 0 && chapter.Number >= plan.StartChapter && (plan.EndChapter == 0 || chapter.Number <= plan.EndChapter) {
				chapters[chapter.ID] = true
			}
		}
	}
	for _, chapter := range after.Chapters {
		if chapters[chapter.ID] {
			result = append(result, ChangeImpact{Kind: "chapter", ID: chapter.ID, Title: chapter.Title})
		}
	}
	return result
}

// UnmarshalJSON supplies collections for old or model-authored location records with omitted optional fields.
func (location *WorldLocation) UnmarshalJSON(data []byte) error {
	type record WorldLocation
	value := record{Kind: "place", EntityIDs: []string{}, PlanIDs: []string{}, ChapterIDs: []string{}}
	if err := decodeRecord(data, &value); err != nil {
		return err
	}
	*location = WorldLocation(value)
	return nil
}

// validateWorldMap checks the final graph so related changes may be committed together in any order.
func validateWorldMap(project *Project) error {
	if len(project.Studio.Locations) > 10000 || len(project.Studio.Routes) > 30000 {
		return errors.New("The world map exceeds its record limit")
	}
	locations := map[string]WorldLocation{}
	names := map[string]bool{}
	entities, plans, chapters := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range project.Entities {
		entities[item.ID] = true
	}
	for _, item := range project.Studio.Plans {
		plans[item.ID] = true
	}
	for _, item := range project.Chapters {
		chapters[item.ID] = true
	}
	for _, item := range project.Studio.Locations {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if item.ID == "" || locations[item.ID].ID != "" || names[name] || name == "" || charCount(item.Name) > 120 || charCount(item.Description) > 12000 || !contains([]string{"place", "region"}, item.Kind) {
			return errors.New("Map locations require unique names and IDs and a valid place or region type")
		}
		if math.IsNaN(item.X) || math.IsNaN(item.Y) || math.IsInf(item.X, 0) || math.IsInf(item.Y, 0) || math.Abs(item.X) > 10000 || math.Abs(item.Y) > 10000 {
			return errors.New("Map coordinates must be finite and within 10,000 units")
		}
		for _, reference := range []struct {
			ids   []string
			owned map[string]bool
		}{{item.EntityIDs, entities}, {item.PlanIDs, plans}, {item.ChapterIDs, chapters}} {
			for _, id := range reference.ids {
				if !reference.owned[id] {
					return errors.New("A map reference is missing from this project")
				}
			}
		}
		locations[item.ID], names[name] = item, true
	}
	for _, item := range project.Studio.Locations {
		seen := map[string]bool{item.ID: true}
		for parent := item.RegionID; parent != ""; {
			region, found := locations[parent]
			if !found || seen[parent] || region.Kind != "region" {
				return errors.New("Map regions must exist and have no containment cycles")
			}
			seen[parent] = true
			parent = region.RegionID
		}
	}
	ids := map[string]bool{}
	for _, route := range project.Studio.Routes {
		if route.ID == "" || ids[route.ID] || route.FromID == route.ToID || locations[route.FromID].ID == "" || locations[route.ToID].ID == "" {
			return errors.New("Routes require unique IDs and two existing locations")
		}
		if math.IsNaN(route.TravelHours) || math.IsInf(route.TravelHours, 0) || route.TravelHours < 0 || route.TravelHours > 87600 || charCount(route.Transport) > 120 || charCount(route.Description) > 12000 {
			return errors.New("Enter a valid route description and travel time between 0 and 87,600 hours")
		}
		ids[route.ID] = true
	}
	return nil
}

// applyWorldMapOperation stages one map mutation on the caller's transactional project copy.
func applyWorldMapOperation(project *Project, op *ChangeOperation) error {
	op.BeforeValue = changeTargetValue(project, *op)
	if string(op.BeforeValue) == "null" {
		op.BeforeValue = nil
	}
	if op.OpType == OpDelete {
		if op.BeforeValue == nil {
			return errors.New("Map target no longer exists")
		}
		removeWorldMapRecord(project, op.TargetKind, op.TargetID)
		return nil
	}
	if op.TargetKind == TargetLocation {
		var location WorldLocation
		if err := decodeJSON(op.AfterValue, &location); err != nil {
			return err
		}
		if location.ID == "" {
			location.ID = op.TargetID
		}
		if location.ID == "" {
			location.ID = newID()
		}
		if op.TargetID != "" && op.TargetID != location.ID {
			return errors.New("Map operation and location IDs differ")
		}
		if location.Kind == "" {
			location.Kind = "place"
		}
		var supplied map[string]json.RawMessage
		if err := decodeJSON(op.AfterValue, &supplied); err != nil {
			return err
		}
		if supplied["x"] == nil && supplied["y"] == nil {
			location.X = float64(100 + len(project.Studio.Locations)%4*180)
			location.Y = float64(90 + len(project.Studio.Locations)/4*140)
		}
		location.Name = strings.TrimSpace(location.Name)
		op.TargetID = location.ID
		op.BeforeValue = changeTargetValue(project, *op)
		if string(op.BeforeValue) == "null" {
			op.BeforeValue = nil
		}
		removeWorldMapRecord(project, TargetLocation, location.ID)
		project.Studio.Locations = append(project.Studio.Locations, location)
		op.AfterValue = rawMessage(location)
		return nil
	}
	var route WorldRoute
	if err := decodeJSON(op.AfterValue, &route); err != nil {
		return err
	}
	if route.ID == "" {
		route.ID = op.TargetID
	}
	if route.ID == "" {
		route.ID = newID()
	}
	if op.TargetID != "" && op.TargetID != route.ID {
		return errors.New("Map operation and route IDs differ")
	}
	op.TargetID = route.ID
	op.BeforeValue = changeTargetValue(project, *op)
	if string(op.BeforeValue) == "null" {
		op.BeforeValue = nil
	}
	removeWorldMapRecord(project, TargetRoute, route.ID)
	project.Studio.Routes = append(project.Studio.Routes, route)
	op.AfterValue = rawMessage(route)
	return nil
}

// removeWorldMapRecord removes one explicitly selected record while final graph validation protects references.
func removeWorldMapRecord(project *Project, kind ChangeTargetKind, id string) {
	if kind == TargetLocation {
		kept := []WorldLocation{}
		for _, item := range project.Studio.Locations {
			if item.ID != id {
				kept = append(kept, item)
			}
		}
		project.Studio.Locations = kept
		return
	}
	kept := []WorldRoute{}
	for _, item := range project.Studio.Routes {
		if item.ID != id {
			kept = append(kept, item)
		}
	}
	project.Studio.Routes = kept
}

// undoWorldMapOperation restores the exact reviewed map value before final cross-record validation.
func undoWorldMapOperation(project *Project, op ChangeOperation) error {
	removeWorldMapRecord(project, op.TargetKind, op.TargetID)
	if len(op.BeforeValue) == 0 || string(op.BeforeValue) == "null" {
		return nil
	}
	if op.TargetKind == TargetLocation {
		var value WorldLocation
		if err := json.Unmarshal(op.BeforeValue, &value); err != nil {
			return err
		}
		project.Studio.Locations = append(project.Studio.Locations, value)
	} else {
		var value WorldRoute
		if err := json.Unmarshal(op.BeforeValue, &value); err != nil {
			return err
		}
		project.Studio.Routes = append(project.Studio.Routes, value)
	}
	return nil
}

// readWorldMap exposes a paged index or a selected record with a paged description to the creative partner.
func readWorldMap(project *Project, step agentStepEnvelope) (string, error) {
	for _, location := range project.Studio.Locations {
		if location.ID == step.EntityID {
			content := []rune(location.Description)
			offset := max(0, min(step.Offset, len(content)))
			end := min(len(content), offset+2400)
			location.Description = string(content[offset:end])
			return jsonText(map[string]any{"location": location, "total_description_chars": len(content), "next_offset_chars": end}), nil
		}
	}
	for _, route := range project.Studio.Routes {
		if route.ID == step.EntityID {
			content := []rune(route.Description)
			offset := max(0, min(step.Offset, len(content)))
			end := min(len(content), offset+2400)
			route.Description = string(content[offset:end])
			return jsonText(map[string]any{"route": route, "total_description_chars": len(content), "next_offset_chars": end}), nil
		}
	}
	locations := append([]WorldLocation{}, project.Studio.Locations...)
	sort.Slice(locations, func(i, j int) bool { return locations[i].ID < locations[j].ID })
	matched := []WorldLocation{}
	query := lowerText(strings.TrimSpace(step.Query))
	for _, item := range locations {
		if query == "" || strings.Contains(lowerText(item.Name+" "+item.Description), query) {
			matched = append(matched, item)
		}
	}
	offset := max(0, min(step.ItemOffset, len(matched)))
	end := min(len(matched), offset+12)
	index := []map[string]any{}
	for _, item := range matched[offset:end] {
		index = append(index, map[string]any{"id": item.ID, "name": item.Name, "kind": item.Kind, "region_id": item.RegionID})
	}
	routeOffset := max(0, min(step.RouteOffset, len(project.Studio.Routes)))
	routeEnd := min(len(project.Studio.Routes), routeOffset+6)
	routes := append([]WorldRoute{}, project.Studio.Routes[routeOffset:routeEnd]...)
	for i := range routes {
		routes[i].Description = ""
	}
	return jsonText(map[string]any{"locations": index, "next_offset": end, "total_locations": len(matched), "routes": routes, "next_route_offset": routeEnd, "total_routes": len(project.Studio.Routes), "detail_tool": "get_world_map with entity_id and offset_chars"}), nil
}

// chapterMapContext supplies relevant travel constraints and explicitly linked places to a scoped writer.
func chapterMapContext(project *Project, chapter *Chapter, query string) string {
	if chapter == nil {
		return ""
	}
	query = lowerText(query + " " + chapter.Title + " " + chapter.Synopsis + " " + chapter.Body)
	selected := map[string]bool{}
	places := []map[string]any{}
	for _, place := range project.Studio.Locations {
		related := contains(place.ChapterIDs, chapter.ID) || strings.Contains(query, lowerText(place.Name))
		for _, plan := range project.Studio.Plans {
			if contains(place.PlanIDs, plan.ID) && plan.StartChapter <= chapter.Number && (plan.EndChapter == 0 || chapter.Number <= plan.EndChapter) {
				related = true
			}
		}
		if related {
			selected[place.ID] = true
			places = append(places, map[string]any{"id": place.ID, "name": place.Name, "region_id": place.RegionID, "description": truncateSummary(place.Description, 400)})
		}
	}
	routes := []WorldRoute{}
	for _, route := range project.Studio.Routes {
		if selected[route.FromID] || selected[route.ToID] {
			routes = append(routes, route)
		}
	}
	if len(places) == 0 {
		return ""
	}
	return "\nAuthor-confirmed map constraints: " + jsonText(map[string]any{"locations": places, "routes": routes})
}
