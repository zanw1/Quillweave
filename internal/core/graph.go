package core

import (
	"fmt"
	"sort"
)

// stringPointer creates independent optional identifier values for JSON.
func stringPointer(value string) *string { return &value }

// buildGraph projects current evidence within chapter scope using stable node and edge IDs.
func buildGraph(project *Project, through uint32, includePending bool) KnowledgeGraph {
	nodes := map[string]*GraphNode{}
	edges := map[string]*GraphEdge{}
	result := KnowledgeGraph{Nodes: []GraphNode{}, Edges: []GraphEdge{}, ThroughChapter: through}
	for _, entity := range project.Entities {
		id := "entity:" + entity.ID
		nodes[id] = &GraphNode{ID: id, Label: entity.Name, Kind: entity.Kind, EntityID: stringPointer(entity.ID), Description: entity.Description, EvidenceIDs: []string{}}
	}
	for _, fact := range project.Facts {
		if fact.Chapter > through || !(fact.Status == "confirmed" || includePending && fact.Status == "pending") {
			continue
		}
		if !factCurrent(project, &fact) {
			result.OmittedStale++
			continue
		}
		subject := project.ResolveEntity(fact.Entity)
		if subject == nil {
			result.UnresolvedRelations++
			continue
		}
		subjectID, sourceID := "entity:"+subject.ID, "source:"+fact.SourceID
		if nodes[sourceID] == nil {
			kind := "source"
			for _, chapter := range project.Chapters {
				if chapter.ID == fact.SourceID {
					kind = "chapter"
					break
				}
			}
			nodes[sourceID] = &GraphNode{ID: sourceID, Label: sourceTitle(project, fact.SourceID), Kind: kind, SourceID: stringPointer(fact.SourceID), Description: "Source text. Select it to view the linked evidence.", EvidenceIDs: []string{}}
		}
		nodes[sourceID].EvidenceIDs = append(nodes[sourceID].EvidenceIDs, fact.ID)
		nodes[subjectID].EvidenceIDs = append(nodes[subjectID].EvidenceIDs, fact.ID)
		addGraphEdge(edges, sourceID, subjectID, "evidence", fact)
		if fact.RelatedEntity != "" {
			other := project.ResolveEntity(fact.RelatedEntity)
			if other == nil {
				result.UnresolvedRelations++
			} else {
				otherID := "entity:" + other.ID
				if otherID != subjectID {
					nodes[otherID].EvidenceIDs = append(nodes[otherID].EvidenceIDs, fact.ID)
					addGraphEdge(edges, sourceID, otherID, "evidence", fact)
					addGraphEdge(edges, subjectID, otherID, "relation", fact)
				}
			}
		}
	}
	for _, id := range sortedKeys(nodes) {
		node := nodes[id]
		node.EvidenceIDs = uniqueSorted(node.EvidenceIDs)
		result.Nodes = append(result.Nodes, *node)
	}
	for _, id := range sortedKeys(edges) {
		edge := edges[id]
		edge.EvidenceIDs = uniqueSorted(edge.EvidenceIDs)
		edge.Label = fmt.Sprintf("%d citations", len(edge.EvidenceIDs))
		if edge.Kind == "relation" {
			edge.Label = fmt.Sprintf("%d relationship records", len(edge.EvidenceIDs))
		}
		result.Edges = append(result.Edges, *edge)
	}
	return result
}

// addGraphEdge aggregates review states and sorts bidirectional identities deterministically.
func addGraphEdge(edges map[string]*GraphEdge, source, target, kind string, fact Fact) {
	if kind == "relation" && source > target {
		source, target = target, source
	}
	id := "edge:" + hashText(source+"|"+target+"|"+kind+"|"+fact.Status)
	if edges[id] == nil {
		edges[id] = &GraphEdge{ID: id, Source: source, Target: target, Kind: kind, Status: fact.Status, EvidenceIDs: []string{}}
	}
	edges[id].EvidenceIDs = append(edges[id].EvidenceIDs, fact.ID)
}

// uniqueSorted sorts and deduplicates a set for projection output.
func uniqueSorted(values []string) []string {
	sort.Strings(values)
	result := []string{}
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
