//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

func TestAliasChangeSetCanBePreviewed(t *testing.T) {
	project := NewProject("未命名作品", "玄幻", "")
	cs := ChangeSet{
		ID: "cs-alias", ProjectID: project.ID, Status: ChangeSetStaged, Source: "agent",
		Operations: []ChangeOperation{
			{ID: "meta", TargetKind: TargetProjectMeta, TargetID: "project_meta", OpType: OpUpsert, Title: "定位", AfterValue: rawMessage(map[string]any{
				"title": "未命名作品（守墓长生流）", "genre": "玄幻修仙", "form": "serial", "pace": "慢节奏",
				"narrative_emphasis": "幕后苟道", "naming_preference": "original",
			})},
			{ID: "goal", TargetKind: TargetStudioGoal, TargetID: "studio_goal", OpType: OpUpsert, Title: "目标", AfterValue: rawMessage(map[string]any{
				"title": "核心创作目标", "content": "主角守墓发育。",
			})},
			{ID: "finger", TargetKind: TargetEntity, TargetID: "ent_gold", OpType: OpUpsert, Title: "金手指", AfterValue: rawMessage(map[string]any{
				"id": "ent_gold", "name": "守墓金手指", "kind": "rule", "aliases": []string{}, "description": "汲取感悟",
			})},
			{ID: "plan", TargetKind: TargetPlan, TargetID: "plan_arc_1", OpType: OpUpsert, Title: "总纲", AfterValue: rawMessage(map[string]any{
				"id": "plan_arc_1", "level": "outline", "title": "总纲主线", "start_chapter": 1, "end_chapter": 0,
				"goal": "保住宗门", "characters": []string{}, "clues": []string{"古墓"},
			})},
		},
	}
	next, err := previewChangeSet(project, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.ReviewToken == "" {
		t.Fatal("preview did not issue a confirmation token")
	}
	for _, op := range next.Operations {
		switch op.TargetKind {
		case TargetEntity:
			var entity Entity
			if err := decodeJSON(op.PreviewValue, &entity); err != nil || entity.Kind != "concept" {
				t.Fatalf("entity kind = %s (%v)", entity.Kind, err)
			}
		case TargetPlan:
			var plan StoryPlan
			if err := decodeJSON(op.PreviewValue, &plan); err != nil || plan.Level != "book" || plan.EndChapter != 0 {
				t.Fatalf("plan = %+v (%v)", plan, err)
			}
		case TargetProjectMeta:
			var meta projectMetaPatch
			if err := decodeJSON(op.PreviewValue, &meta); err != nil || meta.Premise == nil || !strings.Contains(*meta.Premise, "主角守墓发育") {
				t.Fatalf("premise = %+v (%v)", meta.Premise, err)
			}
		case TargetStudioGoal:
			var goal studioGoalPatch
			if err := decodeJSON(op.PreviewValue, &goal); err != nil || goal.Mode == nil || *goal.Mode != "serial" || goal.NarrativeFocus == nil || *goal.NarrativeFocus != "幕后苟道" || goal.Pacing == nil || *goal.Pacing != "慢节奏" {
				t.Fatalf("goal = %+v (%v)", goal, err)
			}
		}
	}
	bad := cs
	bad.ID = "cs-bad-kind"
	bad.Operations = []ChangeOperation{{ID: "bad", TargetKind: TargetEntity, TargetID: "ent_bad", OpType: OpUpsert, AfterValue: rawMessage(map[string]any{
		"id": "ent_bad", "name": "飞船", "kind": "spaceship", "description": "不存在的类型",
	})}}
	if _, err := previewChangeSet(project, bad, nil); err == nil || !strings.Contains(err.Error(), "Invalid entry type: 飞船") {
		t.Fatalf("unknown kind error = %v", err)
	}
}

func TestOpenEndedPlanStaysValid(t *testing.T) {
	state := defaultStudio()
	state.Plans = []StoryPlan{{ID: "book", Level: "book", Title: "总纲", StartChapter: 1, EndChapter: 0, Status: "confirmed"}}
	if err := validateStudio(&state); err != nil {
		t.Fatal(err)
	}
	state.Plans = append(state.Plans, StoryPlan{ID: "volume", ParentID: "book", Level: "volume", Title: "卷一", StartChapter: 1, EndChapter: 4, Status: "confirmed"})
	if err := validateStudio(&state); err != nil {
		t.Fatal(err)
	}
	state.Plans[0].EndChapter = 10
	state.Plans[1].EndChapter = 0
	if err := validateStudio(&state); err == nil || !strings.Contains(err.Error(), "exceeds its parent") {
		t.Fatalf("bounded parent accepted an open child: %v", err)
	}
}
