package core

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

const importedProfileMarker = "\n\n截至第一卷末的正文状态：\n"
const importedLifecycleMarker = "\n\n作者规划的生命周期：\n"

type importedMetadataRepairReport struct {
	PlansFixed            int `json:"plans_fixed"`
	EntitiesMerged        int `json:"entities_merged"`
	SourceCategoriesFixed int `json:"source_categories_fixed"`
	ProfilesMoved         int `json:"profiles_moved"`
	ReferencesUpdated     int `json:"references_updated"`
	RecordsReturned       int `json:"records_returned_to_review"`
	InitialDescriptions   int `json:"initial_descriptions_fixed"`
}

type importedMetadataRepairConfig struct {
	AsOfChapter         uint32                       `json:"as_of_chapter"`
	PrimaryEntities     map[string]string            `json:"primary_entities"`
	InitialDescriptions map[string]string            `json:"initial_descriptions,omitempty"`
	Report              importedMetadataRepairReport `json:"report"`
}

type importedMetadataSnapshot struct {
	Plans     []StoryPlan     `json:"plans"`
	Entities  []Entity        `json:"entities"`
	Facts     []Fact          `json:"facts"`
	Sources   []Source        `json:"sources"`
	Memories  []StoryMemory   `json:"memories"`
	Locations []WorldLocation `json:"locations"`
}

type importedEntityResolver struct {
	byID    map[string]Entity
	byName  map[string]Entity
	names   map[string]string
	ids     map[string]string
	removed map[string]bool
}

// importedMetadata copies only the six metadata groups covered by this repair and its undo.
func importedMetadata(project *Project) importedMetadataSnapshot {
	return importedMetadataSnapshot{Plans: project.Studio.Plans, Entities: project.Entities, Facts: project.Facts,
		Sources: project.Sources, Memories: project.Studio.Memories, Locations: project.Studio.Locations}
}

// restoreImportedMetadata assigns the repair's bounded snapshot without replacing prose or other changesets.
func restoreImportedMetadata(project *Project, snapshot importedMetadataSnapshot) {
	project.Studio.Plans, project.Entities, project.Facts = snapshot.Plans, snapshot.Entities, snapshot.Facts
	project.Sources, project.Studio.Memories, project.Studio.Locations = snapshot.Sources, snapshot.Memories, snapshot.Locations
}

// resolve finds existing identities by stable ID, canonical name or alias without guessing unknown text.
func (resolver importedEntityResolver) resolve(value string) (Entity, bool) {
	if entity, ok := resolver.byID[value]; ok {
		return entity, true
	}
	entity, ok := resolver.byName[value]
	return entity, ok
}

// newImportedEntityResolver follows only the author's explicit merge choices and rejects cycles or missing targets.
func newImportedEntityResolver(project *Project, primary map[string]string) (importedEntityResolver, error) {
	resolver := importedEntityResolver{byID: map[string]Entity{}, byName: map[string]Entity{}, names: map[string]string{}, ids: map[string]string{}, removed: map[string]bool{}}
	for _, entity := range project.Entities {
		resolver.byID[entity.ID] = entity
		resolver.byName[entity.Name] = entity
		for _, alias := range entity.Aliases {
			resolver.byName[alias] = entity
		}
	}
	direct := map[string]string{}
	for source, target := range primary {
		entity, ok := resolver.resolve(target)
		if strings.TrimSpace(source) == "" || !ok {
			return resolver, fmt.Errorf("Imported entity merge requires an existing primary entity: %s", target)
		}
		if old, exists := resolver.resolve(source); exists && old.ID != entity.ID {
			if previous, duplicate := direct[old.ID]; duplicate && previous != entity.ID {
				return resolver, errors.New("Imported entity aliases specify conflicting primary entities")
			}
			direct[old.ID] = entity.ID
		}
	}
	for source, target := range primary {
		entity, _ := resolver.resolve(target)
		seen := map[string]bool{}
		for direct[entity.ID] != "" {
			if seen[entity.ID] {
				return resolver, errors.New("Imported entity merge choices contain a cycle")
			}
			seen[entity.ID] = true
			entity = resolver.byID[direct[entity.ID]]
		}
		resolver.names[source] = entity.Name
		if old, ok := resolver.resolve(source); ok && old.ID != entity.ID {
			resolver.ids[old.ID], resolver.removed[old.ID] = entity.ID, true
			resolver.names[old.Name] = entity.Name
			for _, alias := range old.Aliases {
				resolver.names[alias] = entity.Name
			}
		}
	}
	return resolver, nil
}

// name maps configured old names while preserving unrecognized author text.
func (resolver importedEntityResolver) name(value string) string {
	if name, ok := resolver.names[value]; ok {
		return name
	}
	return value
}

// characterName also decodes existing entity IDs used by the old importer's plan character field.
func (resolver importedEntityResolver) characterName(value string) string {
	if entity, ok := resolver.byID[value]; ok {
		return resolver.name(entity.Name)
	}
	return resolver.name(value)
}

// importedUnique retains encounter order and preserves unknown text while removing duplicate references.
func importedUnique(values []string) []string {
	if values == nil {
		return nil
	}
	result, seen := []string{}, map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

// knownBy splits a compound knowledge attribution only when every named part already resolves.
func (resolver importedEntityResolver) knownBy(values []string) ([]string, bool) {
	if values == nil {
		return nil, false
	}
	result, unresolved := []string{}, false
	for _, value := range values {
		parts := strings.Split(value, "、")
		if len(parts) == 1 {
			result = append(result, resolver.name(value))
			continue
		}
		names, valid := []string{}, true
		for _, part := range parts {
			entity, ok := resolver.resolve(strings.TrimSpace(part))
			if !ok {
				valid = false
				break
			}
			names = append(names, resolver.name(entity.Name))
		}
		if valid {
			result = append(result, names...)
		} else {
			result, unresolved = append(result, value), true
		}
	}
	return importedUnique(result), unresolved
}

// moveImportedProfiles separates the importer's marked end-of-volume notes from timeless entity descriptions.
func moveImportedProfiles(project *Project, asOf uint32, report *importedMetadataRepairReport) error {
	for i := range project.Entities {
		entity := &project.Entities[i]
		start := strings.Index(entity.Description, importedProfileMarker)
		if start < 0 {
			continue
		}
		contentStart := start + len(importedProfileMarker)
		end := len(entity.Description)
		if relative := strings.Index(entity.Description[contentStart:], importedLifecycleMarker); relative >= 0 {
			end = contentStart + relative
		}
		content := strings.TrimSpace(entity.Description[contentStart:end])
		if content != "" {
			id := "import-profile-" + hashText(jsonText([]any{project.ID, entity.ID, asOf}))
			if index := memoryIndex(project, id); index >= 0 {
				if project.Studio.Memories[index].Content != content || project.Studio.Memories[index].Chapter != asOf {
					return errors.New("An imported profile snapshot ID is already used by different memory")
				}
			} else {
				kind := "setting"
				if entity.Kind == "character" {
					kind = "character"
				}
				project.Studio.Memories = append(project.Studio.Memories, StoryMemory{ID: id, Kind: kind, Entity: entity.Name,
					Title: entity.Name + localText(project, " · imported current state", " · 导入的当前状态"), Content: content,
					State: "author_snapshot", Status: "confirmed", Chapter: asOf, KnownBy: []string{}, UpdatedAt: now()})
			}
		}
		entity.Description = entity.Description[:start] + entity.Description[end:]
		report.ProfilesMoved++
	}
	return nil
}

// replaceImportedInitialDescriptions uses explicit author text while retaining the marked future lifecycle suffix.
func replaceImportedInitialDescriptions(project *Project, descriptions map[string]string, report *importedMetadataRepairReport) error {
	for name, content := range descriptions {
		entity := project.ResolveEntity(name)
		if entity == nil {
			return fmt.Errorf("Initial description requires an existing entity: %s", name)
		}
		suffix := ""
		if index := strings.Index(entity.Description, importedLifecycleMarker); index >= 0 {
			suffix = entity.Description[index:]
		}
		next := content + suffix
		if next != entity.Description {
			entity.Description = next
			report.InitialDescriptions++
		}
	}
	return nil
}

// normalizeImportedReferences applies explicit identity repairs and returns affected claims to author review.
func normalizeImportedReferences(project *Project, resolver importedEntityResolver, report *importedMetadataRepairReport) {
	for i := range project.Facts {
		fact := &project.Facts[i]
		entity, related := resolver.name(fact.Entity), resolver.name(fact.RelatedEntity)
		changed := entity != fact.Entity || related != fact.RelatedEntity
		if entity != fact.Entity {
			report.ReferencesUpdated++
		}
		if related != fact.RelatedEntity {
			report.ReferencesUpdated++
		}
		fact.Entity, fact.RelatedEntity = entity, related
		if changed && fact.Status == "confirmed" {
			fact.Status = "pending"
			report.RecordsReturned++
		}
	}
	for i := range project.Studio.Memories {
		memory := &project.Studio.Memories[i]
		entity := resolver.name(memory.Entity)
		known, unresolved := resolver.knownBy(memory.KnownBy)
		changed := entity != memory.Entity || !reflect.DeepEqual(known, memory.KnownBy)
		if entity != memory.Entity {
			report.ReferencesUpdated++
		}
		if !reflect.DeepEqual(known, memory.KnownBy) {
			report.ReferencesUpdated++
		}
		memory.Entity, memory.KnownBy = entity, known
		if (changed || unresolved) && memory.Status == "confirmed" {
			memory.Status = "proposed"
			report.RecordsReturned++
		}
	}
	for i := range project.Studio.Plans {
		plan := &project.Studio.Plans[i]
		names := make([]string, len(plan.Characters))
		for j, name := range plan.Characters {
			names[j] = resolver.characterName(name)
		}
		if plan.Characters == nil {
			names = nil
		}
		names = importedUnique(names)
		if !reflect.DeepEqual(names, plan.Characters) {
			plan.Characters = names
			report.PlansFixed++
			report.ReferencesUpdated++
		}
	}
	for i := range project.Studio.Locations {
		location := &project.Studio.Locations[i]
		ids := append([]string(nil), location.EntityIDs...)
		if location.EntityIDs != nil && ids == nil {
			ids = []string{}
		}
		for j, id := range ids {
			if target, ok := resolver.ids[id]; ok {
				ids[j] = target
			}
		}
		ids = importedUnique(ids)
		if !reflect.DeepEqual(ids, location.EntityIDs) {
			location.EntityIDs = ids
			report.ReferencesUpdated++
		}
	}
	kept := []Entity{}
	for _, entity := range project.Entities {
		if resolver.removed[entity.ID] {
			report.EntitiesMerged++
		} else {
			kept = append(kept, entity)
		}
	}
	if report.EntitiesMerged > 0 {
		project.Entities = kept
	}
}

// applyImportedMetadataRepair repairs selected import contracts and snapshots metadata for exact undo.
func applyImportedMetadataRepair(project *Project, operation *ChangeOperation) error {
	var config importedMetadataRepairConfig
	if err := decodeJSON(operation.AfterValue, &config); err != nil {
		return err
	}
	if config.AsOfChapter == 0 {
		return errors.New("Choose the chapter through which imported current-state notes are valid")
	}
	before := rawMessage(importedMetadata(project))
	var copied importedMetadataSnapshot
	if err := decodeJSON(before, &copied); err != nil {
		return err
	}
	working := *project
	restoreImportedMetadata(&working, copied)
	resolver, err := newImportedEntityResolver(&working, config.PrimaryEntities)
	if err != nil {
		return err
	}
	config.Report = importedMetadataRepairReport{}
	if err := moveImportedProfiles(&working, config.AsOfChapter, &config.Report); err != nil {
		return err
	}
	if err := replaceImportedInitialDescriptions(&working, config.InitialDescriptions, &config.Report); err != nil {
		return err
	}
	normalizeImportedReferences(&working, resolver, &config.Report)
	for i := range working.Sources {
		if working.Sources[i].Category == "style" {
			working.Sources[i].Category = "craft"
			config.Report.SourceCategoriesFixed++
		}
	}
	restoreImportedMetadata(project, importedMetadata(&working))
	operation.BeforeValue, operation.AfterValue = before, rawMessage(config)
	return nil
}

// undoImportedMetadataRepair restores exactly the metadata groups captured by the applied operation.
func undoImportedMetadataRepair(project *Project, operation ChangeOperation) error {
	var snapshot importedMetadataSnapshot
	var fields map[string]any
	if err := decodeJSON(operation.BeforeValue, &fields); err != nil {
		return err
	}
	for _, name := range []string{"plans", "entities", "facts", "sources", "memories", "locations"} {
		if _, ok := fields[name]; !ok {
			return errors.New("Imported metadata repair is missing its undo snapshot")
		}
	}
	if err := decodeJSON(operation.BeforeValue, &snapshot); err != nil {
		return err
	}
	restoreImportedMetadata(project, snapshot)
	return nil
}
