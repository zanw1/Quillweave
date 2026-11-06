package core

import (
	"sort"
	"strings"
)

// creationQuery includes local scene context and planned participants when a request is simply "continue".
func creationQuery(project *Project, chapter *Chapter, instruction string) string {
	parts := []string{instruction}
	if chapter != nil {
		parts = append(parts, chapter.Title, chapter.Synopsis)
		body := []rune(chapter.Body)
		parts = append(parts, string(body[:min(600, len(body))]), string(body[max(0, len(body)-1200):]))
		for _, plan := range project.Studio.Plans {
			if plan.StartChapter <= chapter.Number && plan.EndChapter >= chapter.Number {
				parts = append(parts, strings.Join(plan.Characters, " "), plan.POV, strings.Join(plan.Clues, " "))
			}
		}
	}
	return strings.Join(parts, " ")
}

// relevantEntities preserves full selected biographies and global world notes without loading an entire cast.
func relevantEntities(project *Project, query string, strict bool) []Entity {
	type ranked struct {
		entity Entity
		score  int
	}
	rankedEntities := []ranked{}
	worlds := []Entity{}
	needles := terms(query)
	query = lowerText(query)
	for _, entity := range project.Entities {
		if entity.Kind == "world" && !strict {
			worlds = append(worlds, entity)
			continue
		}
		score := 0
		for _, name := range append([]string{entity.Name}, entity.Aliases...) {
			if name != "" && strings.Contains(query, lowerText(name)) {
				score += 1000
			}
		}
		if score == 0 {
			description := lowerText(entity.Description)
			for term := range needles {
				if len(term) > 2 && strings.Contains(description, term) {
					score++
				}
			}
		}
		if score > 0 || (!strict && strings.TrimSpace(query) == "") {
			rankedEntities = append(rankedEntities, ranked{entity: entity, score: score})
		}
	}
	sort.SliceStable(rankedEntities, func(i, j int) bool { return rankedEntities[i].score > rankedEntities[j].score })
	limit := 12
	named := len(rankedEntities) > 0 && rankedEntities[0].score >= 1000
	if !named {
		limit = 4
	}
	for _, item := range rankedEntities[:min(limit, len(rankedEntities))] {
		if named && item.score < 1000 {
			break
		}
		worlds = append(worlds, item.entity)
	}
	return worlds
}
