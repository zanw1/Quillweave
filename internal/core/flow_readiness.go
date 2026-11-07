package core

import "strings"

type StepStatus string

const (
	StepNotStarted StepStatus = "not_started"
	StepInProgress StepStatus = "in_progress"
	StepReady      StepStatus = "ready"
)

type FlowStepReadiness struct {
	Index  int        `json:"index"`
	Key    string     `json:"key"`
	Status StepStatus `json:"status"`
	Count  int        `json:"count"`
	Hint   string     `json:"hint"`
}

type FlowReadiness struct {
	Steps           [4]FlowStepReadiness `json:"steps"`
	RecommendedStep int                  `json:"recommended_step"`
}

// computeFlowReadiness evaluates the four primary creation areas from current project state.
func computeFlowReadiness(project *Project) FlowReadiness {
	acceptedDecisions := 0
	for _, d := range project.Studio.Decisions {
		if d.Status == "accepted" {
			acceptedDecisions++
		}
	}
	step1Status := StepNotStarted
	step1Count := acceptedDecisions
	hasTitle := strings.TrimSpace(project.Title) != ""
	hasPremise := strings.TrimSpace(project.Premise) != ""
	hasGenre := strings.TrimSpace(project.Genre) != ""
	hasMode := project.Studio.Mode != "" && project.Studio.Mode != "undecided"
	if hasTitle {
		step1Count++
	}
	if hasPremise {
		step1Count++
	}
	if hasTitle && hasMode && (hasPremise || acceptedDecisions > 0) {
		step1Status = StepReady
	} else if hasTitle || hasGenre || hasPremise || len(project.Studio.Decisions) > 0 || hasMode {
		step1Status = StepInProgress
	}

	confirmedMemories, proposedMemories := 0, 0
	for _, m := range project.Studio.Memories {
		switch m.Status {
		case "confirmed":
			confirmedMemories++
		case "proposed":
			proposedMemories++
		}
	}
	confirmedFacts, pendingFacts := 0, 0
	for _, f := range project.Facts {
		switch f.Status {
		case "confirmed":
			confirmedFacts++
		case "pending":
			pendingFacts++
		}
	}
	step2Count := len(project.Entities) + len(project.Studio.Locations) + confirmedMemories + confirmedFacts
	step2Status := StepNotStarted
	if step2Count > 0 {
		step2Status = StepReady
	} else if proposedMemories > 0 || pendingFacts > 0 || len(project.Compilations) > 0 {
		step2Status = StepInProgress
	}

	confirmedPlans := 0
	for _, p := range project.Studio.Plans {
		if p.Status == "confirmed" {
			confirmedPlans++
		}
	}
	step3Count := confirmedPlans
	if strings.TrimSpace(project.Outline) != "" && step3Count == 0 {
		step3Count = 1
	}
	step3Status := StepNotStarted
	if confirmedPlans > 0 || strings.TrimSpace(project.Outline) != "" {
		step3Status = StepReady
	} else if len(project.Studio.Plans) > 0 {
		step3Status = StepInProgress
	}

	writtenChapters := 0
	for _, c := range project.Chapters {
		if strings.TrimSpace(c.Body) != "" {
			writtenChapters++
		}
	}
	step4Status := StepNotStarted
	if writtenChapters > 0 {
		step4Status = StepReady
	} else if len(project.Chapters) > 0 {
		step4Status = StepInProgress
	}

	steps := [4]FlowStepReadiness{
		{
			Index:  1,
			Key:    "setup",
			Status: step1Status,
			Count:  step1Count,
			Hint:   localText(project, "Set story form, core premise, and length targets.", "明确故事形态、核心设定与篇幅目标。"),
		},
		{
			Index:  2,
			Key:    "world",
			Status: step2Status,
			Count:  step2Count,
			Hint:   localText(project, "Build character profiles, world rules, and story memory.", "建立核心角色档案、世界规则与故事记忆。"),
		},
		{
			Index:  3,
			Key:    "plan",
			Status: step3Status,
			Count:  step3Count,
			Hint:   localText(project, "Organize volumes, units, and chapter outlines.", "规划分卷结构、章节目标与冲突因果链。"),
		},
		{
			Index:  4,
			Key:    "write",
			Status: step4Status,
			Count:  writtenChapters,
			Hint:   localText(project, "Write and revise chapter prose with scene context.", "进入沉浸式章节写作、续写与审稿。"),
		},
	}

	recommended := 4
	for i := 0; i < 4; i++ {
		if steps[i].Status != StepReady {
			recommended = steps[i].Index
			break
		}
	}

	return FlowReadiness{
		Steps:           steps,
		RecommendedStep: recommended,
	}
}
