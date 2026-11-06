package core

// taskInLanguage keeps Chinese creative discussions native while preserving protocol and source identifiers.
func taskInLanguage(project *Project, language, action, fallback string) string {
	if responseLanguage(project, language) != "zh-CN" {
		return fallback
	}
	tasks := map[string]string{
		"ask":          "和作者共同打磨故事。先回应想法中值得展开的创意，提出有差异的建议并说明人物选择、冲突和代价，再围绕关键缺口追问一两点。区分原文事实、推断与新方案，引用提供的[C:…]、[S:…]、[W:…]、[M:…]、[N:…]或[T:…]出处。不反复询问已确定的内容，也不急着生成完整大纲。",
		"architecture": "结合讨论完善核心创意、主题、人物欲望与性格矛盾、世界规则、冲突和因果链。指出方案的独特之处与长线发展代价，新增设定标为建议，由作者采纳。",
		"outline":      "依据作者已确认的创意、设定和讨论生成分层规划。尊重目标章数和字数预算，依次处理全书、卷、单元、章节与场景，给出人物弧线、主暗线和阶段转折。每章明确视角、目标、阻碍、选择、后果、伏笔埋设与回收。列出假设与未决问题，规划中的未来不能写成已经发生的前情。",
		"continue":     "续写当前章节，承接已保存的结尾，保持视角、人物声音和此时的知情范围。人物的选择必须遵守世界规则、能力限制及已付出的代价。只输出新增正文，不重复已有结尾。",
		"rewrite":      "按作者要求改写当前章节，返回完整改写正文。保留修改范围之外的情节事实、名字、关系、时间线、伏笔和人物声音；未经要求不添加新能力或改动结局。",
		"review":       "核对人物动机、时间地点、知情范围、世界规则、资源消耗、伏笔和重复表达。每项疑点附原文证据、解释及修改建议；区分倒叙、提及与真实矛盾，不把启发式提示当成确定错误。",
		"agent":        "作为持续协作的创作搭档，先理解作者当前目标，再根据需要调用可用工具，最后给出具体可讨论的创作回应。已有决定直接沿用，候选始终保持待审阅；不自动推进阶段，不声称执行过未发生的搜索或检查。",
	}
	if text, ok := tasks[action]; ok {
		return text
	}
	return fallback
}
