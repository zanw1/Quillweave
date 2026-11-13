package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// StageChangesInput submits a bounded set of non-prose story changes against one saved revision.
type StageChangesInput struct {
	ProjectID    string            `json:"project_id,omitempty"`
	RequestID    string            `json:"request_id"`
	BaseRevision uint64            `json:"base_revision"`
	Summary      string            `json:"summary"`
	Operations   []ChangeOperation `json:"operations"`
}

// CandidateMutation identifies an idempotent candidate withdrawal or recovery request.
type CandidateMutation struct {
	ProjectID   string `json:"project_id,omitempty"`
	RequestID   string `json:"request_id"`
	CandidateID string `json:"candidate_id"`
}

// CandidateConfirmation applies one pending candidate after the author approved its reviewed text.
type CandidateConfirmation struct {
	ProjectID    string `json:"project_id,omitempty"`
	RequestID    string `json:"request_id"`
	CandidateID  string `json:"candidate_id"`
	ConfirmToken string `json:"confirm_token"`
}

// CandidateConflictChoice selects a server-owned recovery effect without accepting edited record values.
type CandidateConflictChoice struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Resolution string `json:"resolution"`
}

// CandidateConflictInput binds author-selected recovery choices to the exact displayed snapshot.
type CandidateConflictInput struct {
	CandidateMutation
	ExpectedRevision uint64                    `json:"expected_revision"`
	ReviewHash       string                    `json:"review_hash"`
	Resolutions      []CandidateConflictChoice `json:"resolutions"`
}

// ResolveCandidateConflicts checkpoints explicit keep/restore choices and invalidates previous confirmation tokens.
func (service *ExternalService) ResolveCandidateConflicts(ctx context.Context, input CandidateConflictInput) (any, error) {
	return service.mutateExternalCandidate(ctx, input.ProjectID, "resolve_candidate_conflicts", input.RequestID, input, func(db *sql.Conn, project *Project) (any, error) {
		candidate := findChangeSet(project, input.CandidateID)
		if candidate == nil {
			return nil, externalFailure("NOT_FOUND", "Candidate not found")
		}
		if candidate.RevertsChangeSetID == "" || !contains([]string{"staged", "conflicted"}, string(candidate.Status)) {
			return nil, externalFailure("INVALID_INPUT", "Choose a pending recovery candidate")
		}
		text, preview, err := candidateReviewText(project, *candidate)
		if err != nil {
			return nil, err
		}
		if project.Revision != input.ExpectedRevision || input.ReviewHash != hashText(text) {
			return nil, externalFailure("CANDIDATE_CHANGED", "Reload the current recovery conflicts before choosing their resolutions")
		}
		if len(input.Resolutions) == 0 {
			return nil, externalFailure("INVALID_INPUT", "Provide at least one conflict choice")
		}
		var effects []ChangeEffect
		if err := decodeJSON(candidate.Operations[0].AfterValue, &effects); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, choice := range input.Resolutions {
			key := choice.Kind + "/" + choice.ID
			if seen[key] || !contains([]string{"keep", "restore"}, choice.Resolution) {
				return nil, externalFailure("INVALID_INPUT", "Choose keep or restore once per conflict")
			}
			seen[key] = true
			found := false
			for _, conflict := range preview.RevertConflicts {
				if conflict.Kind == choice.Kind && conflict.ID == choice.ID {
					found = true
				}
			}
			if !found {
				return nil, externalFailure("INVALID_INPUT", "A selected record is not a current recovery conflict")
			}
			for i := range effects {
				if effects[i].Kind == choice.Kind && effects[i].ID == choice.ID {
					effects[i].Resolution = choice.Resolution
				}
			}
		}
		copy := *candidate
		copy.Operations = append([]ChangeOperation{}, candidate.Operations...)
		copy.Operations[0].AfterValue = rawMessage(effects)
		copy.ReviewToken = ""
		updated, err := previewChangeSet(*project, copy, nil)
		if err != nil {
			return nil, err
		}
		if err := writeChangeSetRecord(db, project.ID, updated); err != nil {
			return nil, err
		}
		upsertChangeSetInProject(project, updated)
		return reviewCandidate(project, ExternalReadRequest{ID: updated.ID})
	})
}

// mutateExternalCandidate commits a candidate operation and its replay receipt atomically.
func (service *ExternalService) mutateExternalCandidate(ctx context.Context, projectID, operation, requestID string, payload any, change func(*sql.Conn, *Project) (any, error)) (any, error) {
	if err := service.writable(); err != nil {
		return nil, err
	}
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	id, err := service.projectID(projectID)
	if err != nil {
		return nil, err
	}
	fingerprint := hashText(jsonText(payload))
	var result any
	err = service.engine.store.withWriteTransaction(func(db *sql.Conn) error {
		if prior, found, err := externalReceipt(db, id, operation, requestID, fingerprint); found || err != nil {
			result = prior
			return err
		}
		project, err := readProject(db, id)
		if err != nil {
			return err
		}
		result, err = change(db, &project)
		if err != nil {
			return err
		}
		return saveExternalReceipt(db, id, operation, requestID, fingerprint, result)
	})
	return result, err
}

// StageChanges stores setting, plan and memory changes while routing prose through writing sessions.
func (service *ExternalService) StageChanges(ctx context.Context, input StageChangesInput) (any, error) {
	return service.mutateExternalCandidate(ctx, input.ProjectID, "stage_changes", input.RequestID, input, func(db *sql.Conn, project *Project) (any, error) {
		if input.BaseRevision != project.Revision {
			return nil, externalFailure("REVISION_CONFLICT", "Reload the project before staging these changes", map[string]any{"current_revision": project.Revision})
		}
		for _, operation := range input.Operations {
			if operation.TargetKind == TargetChapter || contains([]string{"append_prose", "replace_prose", "restore"}, string(operation.OpType)) {
				return nil, externalFailure("PROSE_REQUIRES_WRITING_SESSION", "Submit chapter prose through a writing session")
			}
			if operation.TargetKind == TargetImportMetadata {
				return nil, externalFailure("INVALID_INPUT", "Import repairs use the author import workflow")
			}
		}
		cleaned, err := validateTypedToolOperations(input.Operations)
		if err != nil {
			return nil, externalFailure("INVALID_INPUT", err.Error())
		}
		input.Operations = cleaned
		candidate := ChangeSet{ID: newID(), ProjectID: project.ID, Source: "external", CommitPolicy: "review", BaseRevision: project.Revision, Status: ChangeSetStaged, Summary: input.Summary, Operations: append([]ChangeOperation{}, input.Operations...), CreatedAt: now(), UpdatedAt: now(), Provenance: &ExternalProvenance{Client: service.options.Client, Transport: service.options.Transport}}
		for i := range candidate.Operations {
			candidate.Operations[i].ProposalID = ""
		}
		if err := validateChangeSet(&candidate); err != nil {
			return nil, externalFailure("INVALID_INPUT", err.Error())
		}
		preview, err := previewChangeSet(*project, candidate, nil)
		if err != nil {
			return nil, externalFailure("INVALID_INPUT", err.Error())
		}
		candidate.Operations = preview.Operations
		if err := persistStagedChangeSetsInDB(db, project.ID, []ChangeSet{candidate}); err != nil {
			return nil, err
		}
		return candidate, nil
	})
}

// WithdrawCandidate rejects a pending candidate produced by this external client.
func (service *ExternalService) WithdrawCandidate(ctx context.Context, input CandidateMutation) (any, error) {
	return service.mutateExternalCandidate(ctx, input.ProjectID, "withdraw_candidate", input.RequestID, input, func(db *sql.Conn, project *Project) (any, error) {
		candidate := findChangeSet(project, input.CandidateID)
		if candidate == nil {
			return nil, externalFailure("NOT_FOUND", "Candidate not found")
		}
		if candidate.Provenance == nil || candidate.Provenance.Client != service.options.Client {
			return nil, externalFailure("PROJECT_SCOPE", "Withdraw a candidate produced by this client")
		}
		if !contains([]string{"staged", "conflicted", "rejected"}, string(candidate.Status)) {
			return nil, externalFailure("INVALID_INPUT", "Only pending candidates can be withdrawn")
		}
		candidate.Status, candidate.ReviewToken, candidate.UpdatedAt = ChangeSetRejected, "", now()
		if err := writeChangeSetRecord(db, project.ID, *candidate); err != nil {
			return nil, err
		}
		if err := syncWikiCandidateState(db, project.ID, *candidate); err != nil {
			return nil, err
		}
		return *candidate, nil
	})
}

// PrepareRevert stages a conflict-aware recovery candidate without overwriting later edits.
func (service *ExternalService) PrepareRevert(ctx context.Context, input CandidateMutation) (any, error) {
	return service.mutateExternalCandidate(ctx, input.ProjectID, "prepare_revert", input.RequestID, input, func(db *sql.Conn, project *Project) (any, error) {
		original := findChangeSet(project, input.CandidateID)
		if original == nil {
			return nil, externalFailure("NOT_FOUND", "Original candidate not found")
		}
		if original.Status != ChangeSetApplied {
			return nil, externalFailure("INVALID_INPUT", "Choose an applied candidate to prepare recovery")
		}
		effects := append([]ChangeEffect{}, original.Effects...)
		if len(effects) == 0 {
			undone, copy := cloneProject(*project), *original
			copy.Operations = append([]ChangeOperation{}, original.Operations...)
			if err := undoChangeSetOnProject(&undone, &copy); err != nil {
				return nil, err
			}
			effects = captureChangeEffects(&undone, project)
			for i := range effects {
				effects[i].ForceConflict = project.Revision != original.AppliedRevision
			}
		}
		if len(effects) == 0 {
			return nil, externalFailure("INVALID_INPUT", "The original candidate has no recoverable changes")
		}
		for i := range effects {
			effects[i].Resolution = ""
		}
		candidate := ChangeSet{ID: newID(), ProjectID: project.ID, Source: "external", CommitPolicy: "review", Status: ChangeSetStaged, BaseRevision: project.Revision, Summary: "恢复 · " + original.Summary, RevertsChangeSetID: original.ID, RestoreEffects: effects, CreatedAt: now(), UpdatedAt: now(), Provenance: &ExternalProvenance{Client: service.options.Client, Transport: service.options.Transport}, Operations: []ChangeOperation{{ID: newID(), TargetKind: TargetProjectMeta, TargetID: project.ID, OpType: "restore", Title: "恢复原变更涉及的记录", AfterValue: rawMessage(effects)}}}
		preview, err := previewChangeSet(*project, candidate, nil)
		if err != nil {
			return nil, err
		}
		candidate.RevertConflicts = preview.RevertConflicts
		if err := persistStagedChangeSetsInDB(db, project.ID, []ChangeSet{candidate}); err != nil {
			return nil, err
		}
		return candidate, nil
	})
}

// reviewKindLabels names each target kind the way the app's candidate review does.
var reviewKindLabels = map[ChangeTargetKind]string{
	TargetProjectMeta: "作品基本信息", TargetStudioGoal: "创作目标", TargetDecision: "作者决定", TargetEntity: "角色与世界设定",
	TargetMemory: "故事记忆", TargetPlan: "分层细纲", TargetChapter: "章节正文", TargetLocation: "地点", TargetRoute: "移动路线",
	TargetImportMetadata: "导入作品数据修复",
	TargetWiki:           "Wiki",
}

// reviewCandidate renders a pending candidate as readable per-record changes in Unicode windows.
// The confirm_token appears only in the final window, so an agent reads the whole review before asking the author.
func reviewCandidate(project *Project, request ExternalReadRequest) (any, error) {
	candidate := findChangeSet(project, request.ID)
	if candidate == nil {
		return nil, externalFailure("NOT_FOUND", "Candidate not found")
	}
	if !contains([]string{"staged", "conflicted"}, string(candidate.Status)) {
		return nil, externalFailure("INVALID_INPUT", "Only pending candidates can be reviewed for confirmation; read this record with get_candidate", map[string]any{"status": candidate.Status})
	}
	text, preview, err := candidateReviewText(project, *candidate)
	if err != nil {
		return nil, err
	}
	window, err := externalTextWindow(text, request.Offset, request.MaxChars)
	if err != nil {
		return nil, err
	}
	window["candidate_id"], window["status"], window["summary"] = candidate.ID, candidate.Status, candidate.Summary
	window["revision"], window["operation_count"] = project.Revision, len(candidate.Operations)
	window["review_hash"] = hashText(text)
	conflicts := []map[string]any{}
	for _, conflict := range preview.RevertConflicts {
		conflicts = append(conflicts, map[string]any{"kind": conflict.Kind, "id": conflict.ID, "resolution": conflict.Resolution})
	}
	window["conflicts"], window["conflict_count"] = conflicts[:min(50, len(conflicts))], len(conflicts)
	window["conflicts_truncated"] = len(conflicts) > 50
	if window["truncated"] == true {
		return window, nil
	}
	for _, conflict := range preview.RevertConflicts {
		if conflict.Resolution == "" {
			window["confirm_blocked"] = "Resolve every conflict with resolve_candidate_conflicts, then review the updated candidate"
			return window, nil
		}
	}
	window["confirm_token"] = candidateConfirmToken(project.ID, candidate.ID, text)
	return window, nil
}

// candidateConfirmToken binds an author's approval to the exact review text rather than to unrelated later saves.
func candidateConfirmToken(projectID, candidateID, text string) string {
	return hashText(jsonText([]any{"external-confirm", projectID, candidateID, text}))
}

// candidateReviewText previews a pending candidate without saving it and renders what each operation changes.
// Identities and timestamps are left out, so one candidate on one story revision always renders the same text.
func candidateReviewText(project *Project, candidate ChangeSet) (string, ChangeSet, error) {
	candidate.Operations = append([]ChangeOperation{}, candidate.Operations...)
	preview, err := previewChangeSet(*project, candidate, nil)
	if err != nil {
		return "", candidate, err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "候选：%s\n共 %d 项改动\n", preview.Summary, len(preview.Operations))
	for i, op := range preview.Operations {
		label, before, after := reviewOperationValues(project, op)
		fmt.Fprintf(&text, "\n【%d/%d】%s · %s · %s（%s）\n", i+1, len(preview.Operations), label, reviewKindLabels[op.TargetKind], op.Title, op.TargetID)
		if strings.TrimSpace(op.Summary) != "" {
			fmt.Fprintf(&text, "说明：%s\n", op.Summary)
		}
		if op.TargetKind == TargetWiki {
			if err := writeWikiReview(&text, op); err != nil {
				return "", preview, err
			}
			continue
		}
		if op.OpType != "restore" {
			writeReviewFields(&text, before, after)
			continue
		}
		var effects []ChangeEffect
		if err := decodeJSON(op.AfterValue, &effects); err != nil {
			return "", preview, err
		}
		for _, effect := range effects {
			fmt.Fprintf(&text, "  恢复 %s（%s）\n", effect.Kind, effect.ID)
			writeReviewFields(&text, effect.After, effect.Before)
		}
	}
	for _, conflict := range preview.RevertConflicts {
		fmt.Fprintf(&text, "\n恢复冲突 · %s（%s）：原候选确认后此记录又被修改，处理方式：%s\n", conflict.Kind, conflict.ID, reviewOr(conflict.Resolution, "未选择"))
		writeReviewFields(&text, conflict.Current, conflict.Restore)
	}
	if len(preview.Impacts) > 0 {
		text.WriteString("\n关联影响：\n")
		for _, impact := range preview.Impacts {
			fmt.Fprintf(&text, "  - %s · %s（%s）\n", impact.Kind, impact.Title, impact.ID)
		}
	}
	return text.String(), preview, nil
}

// reviewOperationValues returns one operation's record before and after in the same shape, so only real changes print.
func reviewOperationValues(project *Project, op ChangeOperation) (string, json.RawMessage, json.RawMessage) {
	before, after := changeTargetValue(project, op), op.PreviewValue
	if op.OpType == OpDelete {
		return "删除", before, nil
	}
	if reviewNull(after) {
		after = op.AfterValue
	}
	if reviewNull(before) && op.TargetKind == TargetMemory {
		// A memory edit is saved as a new version; compare it with the version it supersedes.
		var version struct {
			Supersedes string `json:"supersedes"`
		}
		if decodeJSON(after, &version) == nil && version.Supersedes != "" {
			if i := memoryIndex(project, version.Supersedes); i >= 0 {
				before = rawMessage(project.Studio.Memories[i])
			}
		}
	}
	if reviewNull(before) {
		return "新增", nil, after
	}
	return "修改", before, after
}

// reviewNull reports a missing JSON value.
func reviewNull(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// writeReviewFields prints every field of a new or deleted record and only the changed fields of an edited one.
func writeReviewFields(text *strings.Builder, before, after json.RawMessage) {
	old, next := reviewFields(before), reviewFields(after)
	keys := []string{}
	for key := range old {
		keys = append(keys, key)
	}
	for key := range next {
		if _, seen := old[key]; !seen {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if reviewValue(old[key], "") == reviewValue(next[key], "") {
			continue
		}
		if before == nil || after == nil {
			// A new or deleted record lists its fields once; zero numbers and false flags are defaults, not content.
			value := old[key]
			if value == nil {
				value = next[key]
			}
			if value != json.Number("0") && value != false {
				fmt.Fprintf(text, "  %s：%s\n", key, reviewValue(value, "    "))
			}
			continue
		}
		fmt.Fprintf(text, "  %s：\n", key)
		writeReviewChange(text, reviewValue(old[key], "      "), reviewValue(next[key], "      "))
	}
}

// writeReviewChange prints an old and a new value; long texts show only their differing middle,
// so appended prose or a small edit inside a long synopsis is not printed twice.
func writeReviewChange(text *strings.Builder, old, next string) {
	a, b := []rune(old), []rune(next)
	prefix, suffix := 0, 0
	if len(a) > 200 && len(b) > 200 {
		for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
			prefix++
		}
		for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
			suffix++
		}
	}
	if prefix > 0 {
		fmt.Fprintf(text, "    （开头 %d 字相同，略）\n", prefix)
	}
	fmt.Fprintf(text, "    原：%s\n    新：%s\n", reviewOr(string(a[prefix:len(a)-suffix]), "（空）"), reviewOr(string(b[prefix:len(b)-suffix]), "（空）"))
	if suffix > 0 {
		fmt.Fprintf(text, "    （结尾 %d 字相同，略）\n", suffix)
	}
}

// reviewOr substitutes a placeholder for an empty value.
func reviewOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// reviewFields decodes one record into its top-level fields; scalar values are kept under "value".
func reviewFields(raw json.RawMessage) map[string]any {
	fields := map[string]any{}
	if reviewNull(raw) {
		return fields
	}
	var value any
	if decodeJSON(raw, &value) != nil {
		return map[string]any{"value": string(raw)}
	}
	if object, ok := value.(map[string]any); ok {
		for key, item := range object {
			if !reviewHiddenKey(key) {
				fields[key] = item
			}
		}
		return fields
	}
	return map[string]any{"value": value}
}

// reviewHiddenKey skips identities and timestamps, which are not story content and may differ per preview.
func reviewHiddenKey(key string) bool {
	return key == "id" || key == "created_at" || key == "updated_at"
}

// reviewValue renders a decoded JSON value as plain indented lines without quotes or empty fields.
func reviewValue(value any, indent string) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case []any:
		var text strings.Builder
		for _, item := range typed {
			if rendered := reviewValue(item, indent+"  "); rendered != "" {
				text.WriteString("\n" + indent + "- " + strings.TrimPrefix(rendered, "\n"+indent+"  "))
			}
		}
		return text.String()
	case map[string]any:
		keys := []string{}
		for key := range typed {
			if !reviewHiddenKey(key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		var text strings.Builder
		for _, key := range keys {
			if rendered := reviewValue(typed[key], indent+"  "); rendered != "" {
				text.WriteString("\n" + indent + key + "：" + rendered)
			}
		}
		return text.String()
	}
	return fmt.Sprint(value)
}

// ConfirmCandidate applies a pending candidate after the author approved its review in the agent conversation.
// It reuses the desktop preview and apply commands, so every in-app check still runs and the internal review
// token never leaves the server.
func (service *ExternalService) ConfirmCandidate(ctx context.Context, input CandidateConfirmation) (any, error) {
	id, err := service.projectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	activeWriter := service.engine.hasActiveCandidateWriter(id)
	return service.mutateExternalCandidate(ctx, input.ProjectID, "confirm_candidate", input.RequestID, input, func(db *sql.Conn, project *Project) (any, error) {
		candidate := findChangeSet(project, input.CandidateID)
		if candidate == nil {
			return nil, externalFailure("NOT_FOUND", "Candidate not found")
		}
		if candidate.Status == ChangeSetApplied {
			return confirmedCandidateView(*candidate, project.Revision), nil
		}
		if !contains([]string{"staged", "conflicted"}, string(candidate.Status)) {
			return nil, externalFailure("INVALID_INPUT", "Only pending candidates can be confirmed")
		}
		changed := externalFailure("CANDIDATE_CHANGED", "Review and show the current candidate changes before confirming again")
		if !candidateTokenMatches(project, *candidate, input.ConfirmToken) {
			return nil, changed
		}
		preview, err := previewChangeSet(*project, *candidate, nil)
		if err != nil {
			return nil, err
		}
		if !candidateTokenMatches(project, preview, input.ConfirmToken) {
			return nil, changed
		}
		if err := writeChangeSetRecord(db, project.ID, preview); err != nil {
			return nil, err
		}
		args := &arguments{fields: map[string]json.RawMessage{"changeset_id": rawMessage(candidate.ID), "revision": rawMessage(project.Revision), "review_token": rawMessage(preview.ReviewToken)}}
		value, err := service.engine.reviewChangeSetInDB(db, "apply_changeset", project.ID, args, activeWriter)
		if err != nil {
			return nil, err
		}
		receipt, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("The candidate receipt is incomplete")
		}
		applied, ok := receipt["changeset"].(ChangeSet)
		saved, savedOK := receipt["project"].(Project)
		if !ok || !savedOK {
			return nil, errors.New("The candidate receipt is incomplete")
		}
		return confirmedCandidateView(applied, saved.Revision), nil
	})
}

// candidateTokenMatches re-renders the review of a candidate on one story revision and compares its token.
func candidateTokenMatches(project *Project, candidate ChangeSet, token string) bool {
	text, _, err := candidateReviewText(project, candidate)
	return err == nil && token != "" && candidateConfirmToken(project.ID, candidate.ID, text) == token
}

// confirmedCandidateView is the compact reply after confirmation: the new revision and how to undo it.
func confirmedCandidateView(candidate ChangeSet, revision uint64) map[string]any {
	return map[string]any{
		"candidate_id": candidate.ID, "status": candidate.Status, "summary": candidate.Summary, "revision": revision,
		"applied_revision": candidate.AppliedRevision, "operation_count": len(candidate.Operations),
		"undo": "prepare_revert this candidate_id, then review_candidate and confirm_candidate the recovery candidate after the author agrees",
	}
}
