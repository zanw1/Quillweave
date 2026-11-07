package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ChangeTargetKind string

const (
	TargetProjectMeta ChangeTargetKind = "project_meta"
	TargetStudioGoal  ChangeTargetKind = "studio_goal"
	TargetDecision    ChangeTargetKind = "decision"
	TargetEntity      ChangeTargetKind = "entity"
	TargetMemory      ChangeTargetKind = "memory"
	TargetPlan        ChangeTargetKind = "plan"
	TargetChapter     ChangeTargetKind = "chapter"
	TargetLocation    ChangeTargetKind = "location"
	TargetRoute       ChangeTargetKind = "route"
)

type ChangeOpType string

const (
	OpUpsert       ChangeOpType = "upsert"
	OpDelete       ChangeOpType = "delete"
	OpAppendProse  ChangeOpType = "append_prose"
	OpReplaceProse ChangeOpType = "replace_prose"
)

type ChangeSetStatus string

const (
	ChangeSetStaged     ChangeSetStatus = "staged"
	ChangeSetApplied    ChangeSetStatus = "applied"
	ChangeSetUndone     ChangeSetStatus = "undone"
	ChangeSetRejected   ChangeSetStatus = "rejected"
	ChangeSetConflicted ChangeSetStatus = "conflicted"
)

type ChangeOperation struct {
	ID           string           `json:"id"`
	TargetKind   ChangeTargetKind `json:"target_kind"`
	TargetID     string           `json:"target_id"`
	OpType       ChangeOpType     `json:"op_type"`
	Title        string           `json:"title"`
	Summary      string           `json:"summary"`
	ProposalID   string           `json:"proposal_id,omitempty"`
	BeforeValue  json.RawMessage  `json:"before_value,omitempty"`
	PreviewValue json.RawMessage  `json:"preview_value,omitempty"`
	AfterValue   json.RawMessage  `json:"after_value"`
}

type ChangeSet struct {
	ID                   string                  `json:"id"`
	ProjectID            string                  `json:"project_id"`
	SessionID            string                  `json:"session_id,omitempty"`
	TurnID               string                  `json:"turn_id,omitempty"`
	JobID                string                  `json:"job_id,omitempty"`
	ProposalLinksVersion int                     `json:"proposal_links_version,omitempty"`
	Source               string                  `json:"source"`
	CommitPolicy         string                  `json:"commit_policy"`
	Status               ChangeSetStatus         `json:"status"`
	BaseRevision         uint64                  `json:"base_revision"`
	AppliedRevision      uint64                  `json:"applied_revision,omitempty"`
	PreviewRevision      uint64                  `json:"preview_revision"`
	ReviewToken          string                  `json:"review_token,omitempty"`
	BeforeStage          string                  `json:"before_stage,omitempty"`
	BeforeFactReferences []FactReferenceSnapshot `json:"before_fact_references"`
	Skipped              []ChangeOperation       `json:"skipped_operations,omitempty"`
	Impacts              []ChangeImpact          `json:"impacts,omitempty"`
	Archived             bool                    `json:"archived,omitempty"`
	Summary              string                  `json:"summary"`
	Operations           []ChangeOperation       `json:"operations"`
	CreatedAt            string                  `json:"created_at"`
	UpdatedAt            string                  `json:"updated_at"`
}

// FactReferenceSnapshot retains original names only for facts touched by an entity rename.
type FactReferenceSnapshot struct {
	FactID        string `json:"fact_id"`
	Entity        string `json:"entity"`
	RelatedEntity string `json:"related_entity"`
}

type ChangeImpact struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

type WorkspaceContext struct {
	ActiveStep      string `json:"active_step,omitempty"`
	ActiveChapterID string `json:"active_chapter_id,omitempty"`
	ActiveEntityID  string `json:"active_entity_id,omitempty"`
	ActivePlanID    string `json:"active_plan_id,omitempty"`
	ActiveSourceID  string `json:"active_source_id,omitempty"`
	SelectedText    string `json:"selected_text,omitempty"`
}

type projectMetaPatch struct {
	Title               *string `json:"title,omitempty"`
	Genre               *string `json:"genre,omitempty"`
	Premise             *string `json:"premise,omitempty"`
	Style               *string `json:"style,omitempty"`
	Outline             *string `json:"outline,omitempty"`
	WritingStyle        *string `json:"writing_style,omitempty"`
	DefaultChapterWords *uint32 `json:"default_chapter_words,omitempty"`
	NarrativePov        *string `json:"narrative_pov,omitempty"`
}

type studioGoalPatch struct {
	Mode             *string  `json:"mode,omitempty"`
	Stage            *string  `json:"stage,omitempty"`
	TargetCharacters *uint32  `json:"target_characters,omitempty"`
	TargetChapters   *uint32  `json:"target_chapters,omitempty"`
	TargetVolumes    *uint32  `json:"target_volumes,omitempty"`
	Pacing           *string  `json:"pacing,omitempty"`
	NarrativeFocus   *string  `json:"narrative_focus,omitempty"`
	Audience         *string  `json:"audience,omitempty"`
	NamingStyle      *string  `json:"naming_style,omitempty"`
	ForbiddenNames   []string `json:"forbidden_names,omitempty"`
	Preferences      []string `json:"preferences,omitempty"`
}

type chapterProsePatch struct {
	Number   *uint32 `json:"number,omitempty"`
	Title    *string `json:"title,omitempty"`
	Body     string  `json:"body"`
	Synopsis *string `json:"synopsis,omitempty"`
	Mode     string  `json:"mode,omitempty"`
}

// validateChangeSet checks structural invariants before staging or applying a ChangeSet.
func validateChangeSet(cs *ChangeSet) error {
	if cs.ProposalLinksVersion < 0 || cs.ProposalLinksVersion > 1 {
		return errors.New("Unsupported proposal link version")
	}
	if cs.ID == "" {
		cs.ID = newID()
	}
	if cs.Source == "" {
		cs.Source = "agent"
	}
	if !contains([]string{"agent", "extract", "cli", "author"}, cs.Source) {
		return errors.New("Invalid changeset source")
	}
	if !contains([]string{"", "review", "auto"}, cs.CommitPolicy) {
		return errors.New("Invalid changeset commit policy")
	}
	cs.CommitPolicy = "review"
	if cs.Status == "" {
		cs.Status = ChangeSetStaged
	}
	if !contains([]string{"staged", "applied", "undone", "rejected", "conflicted"}, string(cs.Status)) {
		return errors.New("Invalid changeset status")
	}
	if len(cs.Operations) == 0 || len(cs.Operations) > 50 {
		return errors.New("A changeset must contain 1–50 operations")
	}
	ids := map[string]bool{}
	for i := range cs.Operations {
		op := &cs.Operations[i]
		if op.ID == "" {
			op.ID = newID()
		}
		if ids[op.ID] {
			return errors.New("Changeset operation IDs must be unique")
		}
		ids[op.ID] = true
		if !contains([]string{"project_meta", "studio_goal", "decision", "entity", "memory", "plan", "chapter", "location", "route"}, string(op.TargetKind)) {
			return fmt.Errorf("Invalid changeset target kind: %s", op.TargetKind)
		}
		if !contains([]string{"upsert", "delete", "append_prose", "replace_prose"}, string(op.OpType)) {
			return fmt.Errorf("Invalid changeset operation type: %s", op.OpType)
		}
		if (len(op.AfterValue) == 0 || string(op.AfterValue) == "null") && op.OpType != OpDelete {
			return errors.New("Changeset operation requires after_value")
		}
		if op.OpType == OpDelete && !contains([]string{"entity", "memory", "plan", "decision", "location", "route"}, string(op.TargetKind)) {
			return errors.New("This record type does not support deletion")
		}
		if contains([]string{"append_prose", "replace_prose"}, string(op.OpType)) && op.TargetKind != TargetChapter {
			return errors.New("Prose operations require a chapter")
		}
	}
	return nil
}

// isSettingSaveIntent identifies author requests to prepare a setting preview.
func isSettingSaveIntent(instruction string) bool {
	normalized := strings.TrimSpace(instruction)
	for _, phrase := range []string{
		"写进创作目标", "写入创作目标", "保存到创作目标", "写进立项", "写入立项",
		"直接入库", "确认入库", "一键入库", "帮我入库", "直接保存设定",
		"commit directly", "apply directly", "save to studio goals",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

var (
	wanCharRegex = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*万字`)
	chapNumRegex = regexp.MustCompile(`(\d{2,5})\s*章`)
	volNumRegex  = regexp.MustCompile(`(\d{1,3})\s*卷`)
)

// inferGoalOperations extracts structured studio_goal and project_meta patches from natural-language setup text.
func inferGoalOperations(project *Project, title, content string) []ChangeOperation {
	ops := []ChangeOperation{}
	combined := title + "\n" + content
	var goalPatch studioGoalPatch
	changedGoal := false

	if matches := wanCharRegex.FindStringSubmatch(combined); len(matches) == 2 {
		if val, err := strconv.ParseFloat(matches[1], 64); err == nil && val > 0 && val <= 5000 {
			chars := uint32(val * 10000)
			goalPatch.TargetCharacters = &chars
			changedGoal = true
		}
	}
	if matches := chapNumRegex.FindStringSubmatch(combined); len(matches) == 2 {
		if val, err := strconv.ParseUint(matches[1], 10, 32); err == nil && val > 0 && val <= 100000 {
			chaps := uint32(val)
			goalPatch.TargetChapters = &chaps
			changedGoal = true
		}
	}
	if matches := volNumRegex.FindStringSubmatch(combined); len(matches) == 2 {
		if val, err := strconv.ParseUint(matches[1], 10, 32); err == nil && val > 0 && val <= 1000 {
			vols := uint32(val)
			goalPatch.TargetVolumes = &vols
			changedGoal = true
		}
	}
	if project.Studio.Mode == "" || project.Studio.Mode == "undecided" {
		if strings.Contains(combined, "连载") || strings.Contains(combined, "长篇") || strings.Contains(combined, "网文") {
			mode := "serial"
			goalPatch.Mode = &mode
			changedGoal = true
		} else if strings.Contains(combined, "传统") || strings.Contains(combined, "单本") {
			mode := "traditional"
			goalPatch.Mode = &mode
			changedGoal = true
		}
	}
	if changedGoal {
		beforeGoal := studioGoalSnapshot(project)
		ops = append(ops, ChangeOperation{
			ID:          newID(),
			TargetKind:  TargetStudioGoal,
			TargetID:    project.ID,
			OpType:      OpUpsert,
			Title:       localText(project, "Update studio goals & length targets", "更新创作目标与篇幅设定"),
			Summary:     summarizeGoalPatch(project, goalPatch),
			BeforeValue: rawMessage(beforeGoal),
			AfterValue:  rawMessage(goalPatch),
		})
	}

	if strings.TrimSpace(project.Premise) == "" && strings.TrimSpace(content) != "" {
		excerpt := strings.TrimSpace(content)
		if charCount(excerpt) > 600 {
			excerpt = string([]rune(excerpt)[:600])
		}
		metaPatch := projectMetaPatch{Premise: &excerpt}
		beforeMeta := projectMetaPatch{Premise: &project.Premise}
		ops = append(ops, ChangeOperation{
			ID:          newID(),
			TargetKind:  TargetProjectMeta,
			TargetID:    project.ID,
			OpType:      OpUpsert,
			Title:       localText(project, "Update core story premise", "更新作品核心设定摘要"),
			Summary:     truncateSummary(excerpt, 120),
			BeforeValue: rawMessage(beforeMeta),
			AfterValue:  rawMessage(metaPatch),
		})
	}
	return ops
}

// studioGoalSnapshot captures every goal field required for preview and complete undo.
func studioGoalSnapshot(project *Project) studioGoalPatch {
	s := project.Studio
	mode, stage, pacing, focus, audience, naming := s.Mode, s.Stage, s.Pacing, s.NarrativeFocus, s.Audience, s.NamingStyle
	chars, chaps, vols := s.TargetCharacters, s.TargetChapters, s.TargetVolumes
	return studioGoalPatch{
		Mode:             &mode,
		Stage:            &stage,
		TargetCharacters: &chars,
		TargetChapters:   &chaps,
		TargetVolumes:    &vols,
		Pacing:           &pacing,
		NarrativeFocus:   &focus,
		Audience:         &audience,
		NamingStyle:      &naming,
		ForbiddenNames:   append([]string{}, s.ForbiddenNames...),
		Preferences:      append([]string{}, s.Preferences...),
	}
}

// summarizeGoalPatch gives the author a short overview of the changed length and form targets.
func summarizeGoalPatch(project *Project, patch studioGoalPatch) string {
	parts := []string{}
	if patch.Mode != nil {
		parts = append(parts, fmt.Sprintf("mode=%s", *patch.Mode))
	}
	if patch.TargetCharacters != nil {
		parts = append(parts, fmt.Sprintf("target_characters=%d", *patch.TargetCharacters))
	}
	if patch.TargetChapters != nil {
		parts = append(parts, fmt.Sprintf("target_chapters=%d", *patch.TargetChapters))
	}
	if patch.TargetVolumes != nil {
		parts = append(parts, fmt.Sprintf("target_volumes=%d", *patch.TargetVolumes))
	}
	if len(parts) == 0 {
		return localText(project, "Studio goal update", "创作目标更新")
	}
	return strings.Join(parts, ", ")
}

// truncateSummary bounds a display-only label while retaining the full candidate value separately.
func truncateSummary(text string, limit int) string {
	clean := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	runes := []rune(clean)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return clean
}

// proposalsToChangeSet converts StudioProposals into a structured multi-entity ChangeSet.
func proposalsToChangeSet(project *Project, job *Job, proposals []StudioProposal) *ChangeSet {
	if project == nil || len(proposals) == 0 {
		return nil
	}
	ops := []ChangeOperation{}
	for _, p := range proposals {
		first := len(ops)
		switch p.Kind {
		case "outline":
			outline := p.Content
			prev := project.Outline
			ops = append(ops, ChangeOperation{
				ID:          newID(),
				TargetKind:  TargetProjectMeta,
				TargetID:    project.ID,
				OpType:      OpUpsert,
				Title:       p.Title,
				Summary:     truncateSummary(p.Content, 140),
				BeforeValue: rawMessage(projectMetaPatch{Outline: &prev}),
				AfterValue:  rawMessage(projectMetaPatch{Outline: &outline}),
			})
		case "premise":
			premise := p.Content
			prev := project.Premise
			ops = append(ops, ChangeOperation{
				ID:          newID(),
				TargetKind:  TargetProjectMeta,
				TargetID:    project.ID,
				OpType:      OpUpsert,
				Title:       p.Title,
				Summary:     truncateSummary(p.Content, 140),
				BeforeValue: rawMessage(projectMetaPatch{Premise: &prev}),
				AfterValue:  rawMessage(projectMetaPatch{Premise: &premise}),
			})
		case "plan":
			if p.Plan == nil {
				continue
			}
			plan := *p.Plan
			if plan.ID == "" {
				plan.ID = newID()
			}
			plan.Status = "confirmed"
			var before json.RawMessage
			for _, existing := range project.Studio.Plans {
				if existing.ID == plan.ID {
					before = rawMessage(existing)
					break
				}
			}
			ops = append(ops, ChangeOperation{
				ID:          newID(),
				TargetKind:  TargetPlan,
				TargetID:    plan.ID,
				OpType:      OpUpsert,
				Title:       p.Title,
				Summary:     truncateSummary(p.Content+" "+plan.Goal, 140),
				BeforeValue: before,
				AfterValue:  rawMessage(plan),
			})
		case "memory":
			if p.Memory == nil {
				continue
			}
			mem := *p.Memory
			if mem.ID == "" {
				mem.ID = newID()
			}
			mem.Status = "confirmed"
			var before json.RawMessage
			if mem.Supersedes != "" {
				if idx := memoryIndex(project, mem.Supersedes); idx >= 0 {
					before = rawMessage(project.Studio.Memories[idx])
				}
			}
			ops = append(ops, ChangeOperation{
				ID:          newID(),
				TargetKind:  TargetMemory,
				TargetID:    mem.ID,
				OpType:      OpUpsert,
				Title:       p.Title,
				Summary:     truncateSummary(mem.Content, 140),
				BeforeValue: before,
				AfterValue:  rawMessage(mem),
			})
		case "chapter":
			chapterID := ""
			if job != nil {
				chapterID = job.ChapterID
			}
			chapter := findChapter(project, chapterID)
			if chapter == nil {
				continue
			}
			opType := OpAppendProse
			if p.Mode == "replace" {
				opType = OpReplaceProse
			}
			ops = append(ops, ChangeOperation{
				ID:          newID(),
				TargetKind:  TargetChapter,
				TargetID:    chapter.ID,
				OpType:      opType,
				Title:       p.Title,
				Summary:     truncateSummary(p.Content, 140),
				BeforeValue: rawMessage(chapterProsePatch{Body: chapter.Body, Mode: p.Mode}),
				AfterValue:  rawMessage(chapterProsePatch{Body: p.Content, Mode: p.Mode}),
			})
		default:
			sessionID := ""
			instruction := ""
			if job != nil {
				sessionID = job.SessionID
				instruction = job.Instruction
			}
			decision := StoryDecision{
				ID:        newID(),
				Title:     p.Title,
				Content:   p.Content,
				Status:    "accepted",
				SessionID: sessionID,
				CreatedAt: now(),
			}
			ops = append(ops, ChangeOperation{
				ID:         newID(),
				TargetKind: TargetDecision,
				TargetID:   decision.ID,
				OpType:     OpUpsert,
				Title:      p.Title,
				Summary:    truncateSummary(p.Content, 140),
				AfterValue: rawMessage(decision),
			})
			if isSettingSaveIntent(instruction) || strings.Contains(p.Title, "立项") || strings.Contains(p.Title, "目标") {
				ops = append(ops, inferGoalOperations(project, p.Title, p.Content)...)
			}
		}
		for i := first; i < len(ops); i++ {
			ops[i].ProposalID = p.ID
		}
	}
	if len(ops) == 0 {
		return nil
	}
	policy := "review"
	jobID, sessionID := "", ""
	baseRev := project.Revision
	if job != nil {
		jobID = job.ID
		sessionID = job.SessionID
		baseRev = job.BaseRevision
	}
	ts := now()
	return &ChangeSet{
		ID:                   newID(),
		ProjectID:            project.ID,
		SessionID:            sessionID,
		JobID:                jobID,
		ProposalLinksVersion: 1,
		Source:               "agent",
		CommitPolicy:         policy,
		Status:               ChangeSetStaged,
		BaseRevision:         baseRev,
		Summary:              ops[0].Title,
		Operations:           ops,
		CreatedAt:            ts,
		UpdatedAt:            ts,
	}
}
