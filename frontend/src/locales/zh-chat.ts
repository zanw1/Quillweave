/** Localize the conversation composer, model snapshots and catalog-backed commands. */
export const zhChat: Record<string, string> = {
  Undo: "撤销",
  "Effective story knowledge": "有效故事资料",
  "Writing recalls effective rules, character states, relationships, clues and map constraints together. Candidate quotations remain source material, not confirmed facts.": "写作时统一召回有效规则、人物状态、关系、伏笔和地图约束。候选记录提供原文线索，推断不会自动变成确定事实。",
  "Search story knowledge": "搜索人物、地点或事件",
  "Through chapter": "截止章节",
  "Retrieved records": "当前召回",
  "Candidate records": "来源候选",
  "Obsolete or duplicate": "过期或重复",
  "Maintain source records": "整理来源记录",
  "Archive only changed sources, missing quotations and exact duplicates. Interpretations stay separate; archiving can be undone.": "可批量归档来源过期、引文失效和精确重复的候选。语义冲突单独保留，归档后可撤销。",
  "Archive obsolete candidates": "归档过期与重复候选",
  "Source unavailable": "来源已不存在",
  "Source changed": "来源已修改",
  "Quote unavailable": "引文无法定位",
  "Source revision missing": "来源缺少版本绑定",
  "Exact duplicate": "精确重复",
  "State needs checking": "状态可能冲突",
  "Open source or setting": "查看原文或设定",
  "No relevant effective records. Search a character or place name.": "当前范围没有相关有效资料，可用人物或地点名称搜索。",
  "candidate_source": "候选原文",
  "Effective chapters": "生效章节",
  "Unspecified; do not assume everyone knows": "尚未指定，不代表所有人物都知情",
  "Source quote": "原文片段",
  "Evidence type": "依据类型",
  "Source evidence": "原文依据",
  "Save automatically after checks; conflicts remain available for review.": "检查通过后自动保存；冲突结果保留供处理。",
  "Keep generated changes for review before saving.": "生成结果先保留为候选，审阅后保存。",
  "Creation tools": "创作工具",
  "Write chapter": "写章节",
  "Save output": "成果保存",
  "By task": "按任务保存",
  "Save automatically": "自动保存",
  "Review before saving": "审阅后保存",
  "Fiction craft applied · creation commands save automatically; discussion stays for review.":
    "小说写作方法已应用。按任务保存时，创作命令自动入库，讨论建议保留待审。",
  Thinking: "思考中",
  Running: "执行中",
  Duration: "耗时",
  Failed: "失败",
  Interrupted: "已中断",
  "Load skill": "加载技能",
  "Review chapter": "审阅章节",
  "Search web sources": "联网搜索",
  "Thought summary": "思考内容",
  "Recall story context": "检索故事上下文",
  "Read character details": "读取人物设定",
  "Read chapter prose": "读取章节正文",
  "Find story evidence": "查找故事依据",
  "Read story outline": "读取故事大纲",
  "Read world map": "读取世界地图",
  "Read reference material": "读取参考资料",
  "Read author decision": "读取创作决定",
  "Read earlier conversation": "读取历史对话",
  "Read earlier tool result": "读取工具记录",
  "Research story material": "研究故事资料",
  "Write chapter prose": "创作章节正文",
  "Arrange task steps": "安排任务步骤",
  "Prepare proposed changes": "整理修改候选",
  "Check tool request": "检查工具请求",
  "Choose a confirmed chapter outline before writing a new chapter":
    "请先确认要写作的章节大纲，可指定章纲或章号。",
  "Use the writer tool to write the next unwritten chapter from the confirmed chapter outline. Select its plan_id, create the chapter, review the prose and save it.":
    "请调用写作工具，根据已确认的章节大纲选择下一章，自动创建章节，生成并审阅正文后保存。",
  "Future plan": "未来规划",
  "Legacy content": "旧格式内容",
  "Choose Future plan for events that have not happened. Source facts require an original passage.":
    "未发生的章节方案请选择未来规划，原文事实需要对应的原文依据。",
  "Story memory content must be a string": "故事记忆的内容必须是文本。",
  "Story memory text must be a string": "故事记忆的旧格式内容必须是文本。",
  "Story memory content and text conflict. Choose one value before confirming":
    "故事记忆的新旧内容字段不一致，请选定要保留的内容后再确认。",
  "Story memory content is required. Use the content field":
    "故事记忆缺少内容，请填写内容字段。",
  "Story memory ID is required": "故事记忆缺少编号。",
  "Duplicate story memory ID": "故事记忆的编号重复。",
  "Story memory exceeds the 20000-character limit":
    "故事记忆超过20000字符的限制。",
  "Unknown story memory type": "故事记忆类型无法识别，请从支持的类型中选择。",
  "Invalid story memory status": "故事记忆的确认状态无效。",
  "Author-approved future plan; distinguish it from observed story events":
    "作者确认的未来规划，按未来方案使用。",
  "Legacy style instructions will be retained in style notes when saved.":
    "旧版风格指令将在保存时并入风格说明。",
  "Composing the scene": "构思当前场景",
  "Gathering story evidence": "整理写作依据",
  "Revising the candidate": "修订候选正文",
  "Selected composition": "采用的局部构思",
  "Scene notes": "场景安排",
  "Evidence requests": "需要取回的依据",
  "Revelation boundaries": "知情与揭示范围",
  "Open questions": "未决问题",
  "Editing scope prose": "正文表达",
  "Editing scope composition": "局部构思",
  "Editing scope context": "上下文依据",
  "Editing scope plan": "作者规划",
  "Writing model calls": "累计写作调用",
  "Rework composition": "重新构思",
  "Try a short scene": "试写一段",
  "Revise candidate": "修订此候选",
  "Plan flexibility": "规划弹性",
  "Existing author decision": "沿用原确认内容",
  "Locked direction": "作者锁定",
  "Revisable direction": "可讨论调整",
  "Recurring reading experience": "本书持续提供的阅读体验",
  "Describe the activities, relationships, voice and changes readers should want to revisit.":
    "写明希望读者反复留恋的活动、关系、叙述语气和人物变化。",
  "Review this story unit": "回看当前单元",
  "Read the saved prose in this planning scope. Review continuing consequences, independent character choices, repeated payoffs and the original reading experience. Propose only affected future plan changes, retaining unresolved questions.":
    "阅读当前规划范围内已保存的正文，回看持续后果、人物独立选择、重复兑现和本书原定阅读体验。只对受影响的未来节点提出调整候选，保留未决问题。",
  "Try one short alternative scene to explore character voice. Keep it as an unaccepted trial and explain the difference briefly.":
    "试写一个简短的不同版本，探索人物声音，保持草案身份，并简要说明写法差异。",
  "Reconsider the local composition from character goals, refusals, practical conditions and consequences, then write a fresh candidate. Respect locked author decisions.":
    "结合人物目标、拒绝、执行条件与后果，重新构思并写出新的正文候选，遵守作者锁定的决定。",
  "Revise the complete candidate using the located editing feedback. Preserve successful passages and facts outside the affected area.":
    "结合能定位到原文的编辑意见修订完整候选，保留有效段落和修改范围外的事实。",
  "Source task": "来源任务",
  "Candidate fingerprint": "候选版本指纹",
  "Select a completed candidate from this work":
    "请选择本作品中已经完成的候选任务。",
  "The candidate is empty": "候选正文为空。",
  "The evaluation candidate differs from its saved task":
    "评估正文与来源任务中的候选版本不同。",
  "The composition did not return an intent and approach. Retry the saved composition stage":
    "构思阶段未返回完整的意图与写法，可从已保存阶段重试。",
  "The composition exceeds the local planning limit. Use at most four focused evidence requests":
    "局部构思超过支持范围，请使用至多四项明确的取材请求。",
  "The selected composition is missing": "当前任务缺少已选定的局部构思。",
  "Unknown saved writing stage": "无法识别已保存的写作阶段。",
  "Writing needs a composition, draft and review call. Resume with sufficient remaining budget":
    "新构思需要预留构思、正文与审阅三次调用，当前成果已保存，可在预算充足时继续。",
  "Choose a valid plan flexibility and at most 32 open questions":
    "请选择有效的规划弹性，未决问题最多32项。",
  "Model capacity is unknown. The saved local context and output budgets apply.":
    "模型容量尚未确认，当前使用已配置的本地上下文与输出预算。",
  "The current request and required context do not fit the selected model. Remove context or select a larger model.":
    "当前请求与必需材料超过所选模型容量，请调整携带内容或选择容量更大的模型。",
  "Writing stages share this saved version": "写作各阶段使用这一已保存版本",
  "Current chapter planning path; complete goals, choices and consequences":
    "本章关联的完整规划，包含目标、选择与后果",
  "Complete immediate predecessor for scene and relationship continuity":
    "完整前章，用于衔接场景与人物关系",
  "Frozen parent evidence; fail explicitly if the specialist cannot receive it":
    "已固定的父任务证据，容量不足时明确提示",
  "Craft reference with a separate identity from this story's evidence":
    "写作参考单独标识，与本书事实区分",
  "Author-confirmed story memory; respect time and character knowledge":
    "作者已确认的作品记忆，按时间与知情范围使用",
  "Current author-confirmed source evidence": "当前有效且经作者确认的原文依据",
  "The chapter changed during writing. Review the current version before continuing":
    "章节在写作期间发生变化，请核对当前版本后继续。",
  "The story changed during writing. Rebuild this chapter's context before continuing":
    "作品在写作期间发生变化，请重新整理本章上下文。",
  "Writing evidence changed: {0}. Rebuild this chapter's context":
    "写作依据已变化，涉及“{0}”，请重新整理本章上下文。",
  "Writing plan changed: {0}. Rebuild this chapter's context":
    "关联规划已变化，涉及“{0}”，请重新整理本章上下文。",
  "Unable to stage the writing candidate. The draft is retained in the task":
    "无法建立待审正文，草稿已保留在任务中。",
  "The writing task needs a drafting call and a review call":
    "本次写作需要预留正文生成与审阅两次模型调用。",
  "Writing instructions and required evidence exceed the model context budget":
    "写作指令与必需依据超过当前模型上下文预算。",
  "The writer returned an empty candidate": "写作模型返回了空正文。",
  "Model call budget exhausted before writing review":
    "模型调用预算已用尽，正文审阅尚未完成。",
  "Analyze writing references with /reference. Story Wiki compilation uses this work's prose and world sources":
    "请使用 /reference 分析写作参考，作品 Wiki 用于整理本书正文与世界资料。",
  "Saved project revision": "已保存的作品版本",
  "The reference file exceeds 100 MB.":
    "参考文件超过 100 MB，请选择较小的文本。",
  "Unable to decode this file as UTF-8, UTF-16 or GB18030 text.":
    "无法按 UTF-8、UTF-16 或 GB18030 解码此文件。",
  "Detected encoding": "识别编码",
  "Detected chapters": "识别到章节",
  "Choose a chapter range to preserve original line references. Duplicate headings keep their original order.":
    "选择要导入的章节范围，保留原文行号。重复章号按文件顺序分别保留。",
  "First chapter position": "起始章节位置",
  "Last chapter position": "结束章节位置",
  "Plan the current scope. Units and scenes are optional.":
    "按当前目标规划，单元与场景按需使用。",
  "Plan directory": "规划目录",
  "Expand plan": "展开规划",
  "Collapse plan": "折叠规划",
  "Choice / action": "选择与行动",
  "To be developed": "待补充",
  "Target length not set": "目标字数未设定",
  "Write this chapter": "写作当前章节",
  "Review this chapter": "审阅当前章节",
  "Analyze writing references": "分析写作参考",
  "Draft and review this chapter using its planning and saved context":
    "结合章纲与已保存前情，生成并审阅本章正文",
  "Review continuity and reading experience with source passages":
    "结合原文审阅连续性与阅读体验",
  "Trace a reference scene's causes, choices and consequences":
    "分析参考场景的起因、人物选择与持续后果",
  "Write the next complete scene using this chapter's planning and saved context, then review its continuity and reading experience.":
    "结合本章规划、已保存的前情与人物状态，写出下一个完整场景，再审阅连续性与阅读体验。",
  "Review the current chapter's continuity and reading experience. Locate each issue in the prose and suggest a focused edit.":
    "审阅当前章节的连续性与阅读体验，每项意见定位到正文，并提出针对性修改建议。",
  "Read the selected writing reference. Analyze its starting situation, character goals, choices, feasible actions, reactions and lasting consequences. Cite the actual source passages and explain its applicability to this story. Keep the reference's characters and events separate from this work.":
    "阅读所选写作参考，分析起始处境、人物目标、选择、行动条件、反应与持续后果，引用实际原文，说明对本书的适用条件。参考人物和事件保留其来源身份。",
  "Open a chapter before using this command.": "请先打开要处理的章节。",
  "Writing workflow": "写作流程",
  "Chapter brief": "本章构思",
  "Writing prose": "写作正文",
  "Reviewing the candidate": "审阅候选正文",
  "Ready for author review": "待作者采纳",
  "Editing feedback": "编辑意见",
  "Reading experience": "阅读体验",
  "Actual stage inputs": "实际阶段输入",
  Stage: "阶段",
  think: "思考",
  "Required evidence": "必需材料",
  "Source version": "来源版本",
  "Writing and review complete. Preview the prose and memory candidates":
    "写作与审阅已完成，可预览正文及记忆候选。",
  "Writing stage · brief": "正在整理本章构思",
  "Writing stage · drafting": "正在写作正文",
  "Writing stage · reviewing": "正在审阅候选正文",
  "Writing stage · ready": "等待作者采纳",
  Clear: "清除",
  Done: "完成",
  Model: "模型",
  "Choose model": "选择模型",
  "Choose the model for the next submission": "选择下一次提交使用的模型",
  "Model for this conversation": "本会话模型",
  "Conversation model": "会话模型",
  "Conversation reasoning effort": "会话推理档位",
  "Conversation output mode": "会话输出模式",
  "Changes apply to the next submission. Every reply keeps the model that actually generated it.":
    "选择在下一次提交时生效，每条回复保留实际生成它的模型。",
  "Search models": "搜索模型",
  "Model default": "模型默认",
  "Output budget": "输出上限",
  "Automatic from model capacity": "按模型容量自动计算",
  "Custom output tokens": "自定义输出 tokens",
  "Context window": "上下文窗口",
  "Manage connections": "管理连接",
  "Inspect context": "查看上下文",
  "Inspect the actual request and token budget": "查看实际请求与 token 预算",
  "Compact conversation": "压缩会话",
  "Save a checkpoint of earlier complete exchanges":
    "将较早的完整回合保存为检查点",
  "Start a conversation with its own draft": "创建拥有独立草稿的新会话",
  "Search and resume a saved conversation": "搜索并恢复已保存的会话",
  "Compare a few story directions": "比较几个故事方向",
  "Explore titles and character names": "探讨书名与人物命名",
  "Break the discussion into a reviewable outline": "将讨论整理成可审阅的细纲",
  "Polish the saved chapter for author review": "润色章节，生成可审阅的候选",
  "Prepare structured changes for confirmation": "整理为等待确认的结构化修改",
  "Compile source evidence for review": "编纂原文证据并交给作者审阅",
  "Web search": "联网搜索",
  "Choose an available service and request research": "选择可用服务并发起检索",
  Skills: "技能",
  "Attach a skill by name or choose from the catalog":
    "按名称引用技能，或从目录中选择",
  Conversation: "会话",
  Writing: "创作",
  Tools: "工具",
  "Write a message, or type / for commands…": "写下想法，或输入 / 调用命令…",
  "Chat commands": "聊天命令",
  "Type / for commands and skills": "输入 / 使用命令与技能",
  "Remove skill": "移除技能",
  Context: "上下文",
  "Earlier exchanges will be compacted before sending.":
    "发送前会压缩较早的完整对话。",
  "Back to latest": "回到最新回复",
  "Load task details": "加载任务详情",
  "View the running task": "查看正在运行的任务",
  "Search for next submission": "下一次提交使用已选搜索配置",
  "Search all saved conversations": "搜索全部已保存的会话",
  "Archived conversations": "已归档的会话",
  "Restore conversation": "恢复会话",
  "Archive conversation": "归档会话",
  "No conversations found": "没有找到会话",
  "Loading…": "正在加载…",
  "Choose the service for your next submission. Actual searches and citations appear with the reply.":
    "为下一次提交选择搜索服务，实际查询与引用会显示在回复中。",
  "Prepare search request": "准备搜索请求",
  "Selected skills load their instructions into this turn. Their source and version remain attached to the request.":
    "所选技能的指令会加载到本轮，来源与版本随请求保留。",
  "Search skills": "搜索技能",
  "No skills discovered. Add a directory containing skill folders below.":
    "没有发现技能，可在下方添加包含技能文件夹的目录。",
  "Skill directories": "技能目录",
  "One directory per line": "每行一个目录",
  "Save and reload skills": "保存并重新发现技能",
  "Unknown command /{0}. Your draft is kept.": "未知命令 /{0}，草稿已保留。",
  "Model not found. Choose an enabled connection.":
    "没有找到模型，请选择一个已启用的连接。",
  "Choose a unique skill from the catalog.":
    "请从目录中选择一个明确的技能来源。",
  "Search the web for this question and cite the sources: {0}":
    "请联网检索这个问题并引用来源：{0}",
  "Save a conversation checkpoint, then briefly state the remaining goal and open questions.":
    "请保存会话检查点，然后简要说明接下来要推进的目标与待确认的问题。",
  "Open a chapter with prose before using this command.":
    "请先打开一篇已有正文的章节，再使用此命令。",
  "Advanced background model routing": "高级后台模型分配",
  "Chat submissions use the conversation model selected in the composer. These routes remain available for older sessions and background defaults.":
    "聊天使用输入框中选择的会话模型。以下分配保留为旧会话与后台任务的默认设置。",
  "Conversation changed while creating a new session. Your draft is kept.":
    "新建过程中已切换会话，草稿已保留。",
  "Conversation changed while creating a branch. Your draft is kept.":
    "创建分支时已切换会话，草稿已保留。",
  "Conversation changed while preparing the request. Your draft is kept.":
    "准备请求时已切换会话，草稿已保留。",
  "External author": "外部创作来源",
  "Prepared by": "操作来源",
  "Recovery target": "恢复目标",
  "Recovery candidate": "恢复候选",
  "Recovery candidate prepared": "恢复候选已生成",
  "Review it in the project overview.": "请到作品页审阅待确认修改。",
  "Writing session": "写作会话",
  "External review": "外部审阅",
  "Revision rounds": "修订轮次",
  "Record absent": "该记录不存在",
  "Current value": "当前值",
  "Value after original change": "原变更应用后的值",
  "Value to restore": "待恢复的值",
  "Recovery decision": "恢复选择",
  "Choose a recovery decision": "请选择如何处理后续修改",
  "Keep current value": "保留当前值",
  "Restore previous value": "恢复原值",
  "Prepare recovery candidate": "生成恢复候选",
};
