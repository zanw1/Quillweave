package core

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// writingCraftVersion identifies the built-in, source-independent writing methods used by each stage.
const writingCraftVersion = "2026-11-voice-2"

// WritingCraftCard makes a method's trigger, action and misuse visible to the stage that needs it.
type WritingCraftCard struct {
	Trigger        string `json:"trigger"`
	Action         string `json:"action"`
	Counterexample string `json:"counterexample"`
}

// writingCraftCards selects concrete interventions for planning and revision.
// Prose gets no cards: it follows the composition for events and the voice samples for wording.
func writingCraftCards(project *Project, stage string) []WritingCraftCard {
	if stage == "prose" {
		return nil
	}
	cards := []WritingCraftCard{{
		Trigger:        localText(project, "A character acts without a present reason.", "人物像在照着作者安排办事，读者不明白他为什么非做不可。"),
		Action:         localText(project, "Show present wants, costs of inaction and changed choices. Use history only when it affects the decision.", "写出人物此刻想要什么，不做会失去什么。让相似处境中的不同选择表现成长，过往经历只补充影响当下判断的部分。"),
		Counterexample: localText(project, "A biography replaces the immediate reason to act.", "用整段身世介绍代替当下动机，或让所有人无条件替主角办事。"),
	}}
	switch stage {
	case "outline", "composition":
		cards = append(cards, WritingCraftCard{
			Trigger:        localText(project, "Supporting characters only deliver information, praise or rewards.", "配角只负责交情报、夸主角或送奖励。"),
			Action:         localText(project, "Give participants separate wants, knowledge, limits and ongoing lives. A refusal, condition or mistaken interpretation must change an action or obligation. Shared experience changes later choices without guaranteeing loyalty.", "为参与者保留自己的生活、诉求、所知和帮不上忙的地方。拒绝、条件或误判要改变行动或责任；共同经历影响后来的选择，受助不等于从此无条件归心。"),
			Counterexample: localText(project, "Everyone infers the protagonist's importance and agrees immediately.", "众人轮流惊叹，再立即赞同同一个方案。"),
		})
		if stage == "composition" {
			cards = append(cards, WritingCraftCard{
				Trigger:        localText(project, "A quiet chapter follows a success or transition.", "胜利后的日常、过渡或离别需要展开。"),
				Action:         localText(project, "Show gains entering daily life, lasting costs and emotional recovery. Repeated objects or words need changed meaning.", "让真正到手的收益进入生活，伤势和未完责任继续生效。旧物或旧话再次出现时改变用途、含义或相处方式。"),
				Counterexample: localText(project, "A new crisis interrupts every moment of relief, or the same outcome is explained again.", "每次放松都临时塞入敌人，或重复讲述前章已经完成的结果。"),
			})
		}
	case "review":
		cards = append(cards, WritingCraftCard{
			Trigger:        localText(project, "An existing passage needs revision.", "需要修改已写的段落、场景或章纲。"),
			Action:         localText(project, "Preserve established information, clues and current voices. Locate the smallest affected layer. Check whether new evidence corrects an earlier belief, rather than treating both as facts. Check changed actions and emotional processing across adjacent scenes.", "保留已抛信息、伏笔与当前人物声音，定位到受影响的最小层次。新证据推翻旧判断时更新认知，别把两种说法都当事实。检查相邻场景改变了什么，删去重复转述。"),
			Counterexample: localText(project, "A complete rewrite loses established clues and imposes a fixed cliffhanger quota.", "整章重写抹掉已抛信息，再用固定钩子配额补节奏。"),
		})
	}
	return cards
}

// writingCraftGuidance assembles stage guidance without loading reference novels at runtime.
// Planning and review carry the story-logic rules; prose carries only voice guidance.
func writingCraftGuidance(project *Project, stage string) string {
	parts := []string{}
	if stage != "prose" {
		parts = append(parts, writingStoryLogic(project))
	}
	parts = append(parts, writingStageCraft(project, stage))
	if cards := writingCraftCards(project, stage); len(cards) > 0 {
		parts = append(parts, jsonText(cards))
	}
	parts = append(parts, writingLanguageGuidance(project))
	return "<builtin_writing_craft version=\"" + writingCraftVersion + "\" stage=\"" + stage + "\">\n" + strings.Join(parts, "\n") + "\n</builtin_writing_craft>"
}

// writingStoryLogic keeps causality, knowledge, resource and conflict rules for planning and review.
func writingStoryLogic(project *Project) string {
	return localText(project,
		`Use individual wants, knowledge and limits. Helpers have lives and may refuse. Development changes choices. Distinguish promised, received, usable and spent resources; injuries and obligations persist. Separate facts, beliefs, reader revelations and plans. Misunderstandings need evidence and must change actions. Conflict concerns goals or values, without romantic rivalry. Quiet scenes can change relationships. Repeated objects or phrases need new meaning. Cut repeated checks, explanations and spectator amazement.`,
		`围绕人物想做的事展开。人物各有目标、顾虑、所知与底线，帮手也有自己的生活和难处。拒绝、条件和误判要改变行动与关系。人物成长表现为相似处境中的不同选择，受助、敌对或胜利都不能替人物定下一辈子的态度。
情节由已发生的事和可理解的选择推进。能力、道具、路线与资源有来历和限制，分清答应给、已到手、当前能用和已经消耗。伤势、人情与行动后果延续到后场。误解依据当事人能看到的信息和自身愿望，产生新的行动、风险或责任；分清人物所知、读者所知、客观事实与未来计划。
竞争围绕责任、资源、规则、秘密、立场和价值选择展开。杜绝争夺异性关注或伴侣归属的雌竞、雄竞情节。人物之间可以合作、欣赏、分歧和相互影响。
每场戏让行动条件、信息、关系、能力或承诺至少一项有所变化，日常、告别和休息也可承担这些作用。爽点落在办成一件事、得到具体回报、关系回应或认知改变上。旧物、旧话重现要有新含义；换一批旁观者重复惊叹不算变化。按人物处境选择节奏，删去重复讲解、重复核单、无效复核和重复兑现。`)
}

// writingStageCraft returns the method text for one stage in the project's language.
func writingStageCraft(project *Project, stage string) string {
	if responseLanguage(project, "") != "zh-CN" {
		return map[string]string{
			"outline":     "Keep distant milestones and dependencies flexible; detail near-term starting conditions, choices, resulting changes and handoffs. Preserve confirmed commitments. Choose reversals and suspense only when supported by this story; vary adjacent chapter payoffs.",
			"composition": "Choose one or two useful methods in approach. Create compact cards with viewpoint, place, wants, obstacles, evidence, choices, actions, costs, changes and handoff. Cards fix what happens, who decides what and what changes; they do not script lines or gestures. A participant's response is what they actually do here and may simply be going along, watching or one remark; not everyone needs to refuse or set terms. Check aliases, controlled bodies and omitted alternatives without identity stereotypes. Reversals need prior clues and new evidence that changes action. Quiet work, care and farewells can carry change.",
			"prose":       "Match the narrative voice of the earlier chapters, then follow the cards for what happens and what changes; how lines are spoken and gestures are written follows each character's temperament and mood. The viewpoint character's private thoughts, calculations and wry asides are part of that voice: write them into the narration at the weight the earlier prose and voice samples use; this is not summarizing emotion for the reader. Dialogue is people talking with their own habits and present mood; they may interrupt, complain, joke or trail off. Convey necessary information without making every line deliver information or giving every line a gesture. Dramatize key actions and choices; summarize routine time. Avoid romantic-rivalry beats. Keep card and review vocabulary out of the prose.",
			"review":      "Separate source-supported continuity or explicit author constraints from editorial preferences. Locate candidate quotes and exact saved evidence for contradictions. The viewpoint character's exaggerations, jokes, complaints and guesses about others are character voice, not narrated fact or narration summarizing emotion; raise one as continuity only when the character takes as true something that contradicts saved prose, or knows what the story says they cannot know. Flag dialogue that reads like an information hand-off, or narration whose voice departs from the earlier chapters; quote it and suggest a local fix. Explain reading effects and narrow suggestions while preserving effective passages and the character's tone; do not cut a character's jokes or calculations for subtlety, closure or deduplication. Respect quiet scenes. One model's review is an opinion; revise at most once.",
		}[stage]
	}
	return map[string]string{
		"outline":     `规划先确定主角的长期追求、人物关系变化和阶段性结果。远期写里程碑、必要条件、待兑现承诺和可调整的问题；近期才细化到可写的场景。每条近期章纲说清起始处境、人物为什么行动、做了什么选择、结束后改变了什么、下一步从何而来。已经确认的事件作为约束，仍有变数的部分留出调整空间。反转、危机、打脸和章末悬念均按需要使用，避免固定配额与相邻章节复刻同一套路。`,
		"composition": `把本章目标落实成精简场景卡，在approach中只选最有用的一两种方法，说明怎样影响当前人物的选择。每场写清视角、地点、诉求、阻碍、依据、选择、动作、付出、结果与承接。场景卡只定发生什么、谁做了什么决定、结果改变了什么，不替正文设计台词和小动作。参与者的反应写成这场里实际会做的事，可以只是顺从、旁观或一句话，不必人人拒绝或提条件；避免安排全员讲设定或人生道理。涉及化名、冒充或控制时分清各自身体和身份；推理检查遗漏的可能，结论碰巧正确不能证明身份偏见成立。反转须有旧线索、旧判断和改变行动的新证据，线索不足保留问题。安静场景可用共同做事、承诺、照顾或告别承载变化。`,
		"prose": `先对齐前文的叙述声音，再按构思卡落实发生什么、结果是什么；台词怎么说、动作怎么写，按人物性格和当时情绪来。
视角人物的心里话、盘算和吐槽是人物声音的一部分，按前文和文风样本的分量直接写进叙述，这不算替读者总结情绪。
对白是人在说话：带着各自的性格、口头习惯和当下情绪，可以打岔、抱怨、开玩笑、说半截话。必要信息交代清楚即可，不必句句传递信息，也不必每句前面配一个小动作。
关键行动和选择展开写，重复劳动与无变化的时间一笔带过。不写争夺异性关注的雌竞、雄竞桥段。场景卡和审稿术语只用于工作过程，不写进正文。`,
		"review": `先核对可定位的连续性与作者明确约束，再给阅读建议。连续性问题须同时引用候选句和已保存依据，指出具体矛盾；缺少证据保留不确定性。视角人物心里的夸张、玩笑、抱怨和对别人心思的猜测是人物声音，不当作叙述事实核对，也不算旁白替读者总结情绪；只有人物把与已保存正文矛盾的事当真，或知道了按情节不该知道的事，才作为连续性问题提出。对白生硬、像在交接信息，或叙述声音明显不像前文的，指出原句并给出局部改法。人物动机、节奏和语感意见说明影响并提出局部改法，保留有效段落和人物口气，不为留白、收束或去重删掉人物的玩笑和盘算。不要因日常、温情、告别没有危机就判错，也不要要求每章新增反转或强行钩子。相同模型的一次审读只能提供意见，最多修订一次，不能证明小说好看。`,
	}[stage]
}

// writingLanguageGuidance preserves the requested natural Chinese and names concrete editing targets.
func writingLanguageGuidance(project *Project) string {
	if responseLanguage(project, "") != "zh-CN" {
		return "Use everyday, precise language and character-specific voices. Remove stock transitions, empty intensifiers, redundant explanations and robotic summaries. Preserve deliberate rhythm and necessary terms."
	}
	return "中文采用通俗、自然、接地气的叙述，简洁用词，术语随当前行动解释。禁止把以下机械衔接表达写入新正文，用动作、说话或明确时间地点自然连接各句。" + strings.Join(writingBannedTransitions(), "、") + "。删去重复转述、空泛感叹、套路式收尾和作者代人物下结论，纠正错字与病句。"
}

// writingBannedTransitions lists the author's explicit prose constraints for prompts and local checks.
func writingBannedTransitions() []string {
	return []string{"只见", "只见他", "就在这时", "片刻后", "随即", "于是", "因此", "不由得", "不禁", "似乎", "仿佛", "好像", "似乎是", "仿佛是"}
}

// appendWritingLanguageIssues locates the author's explicit banned phrases in new Chinese prose.
// It reports at most five distinct, non-overlapping phrases and leaves the source wording intact.
func appendWritingLanguageIssues(project *Project, body string, review *WritingReview) {
	if review == nil || responseLanguage(project, "") != "zh-CN" {
		return
	}
	phrases := writingBannedTransitions()
	sort.SliceStable(phrases, func(i, j int) bool { return len(phrases[i]) > len(phrases[j]) })
	matched := []struct{ start, end int }{}
	for _, phrase := range phrases {
		position := strings.Index(body, phrase)
		if position < 0 {
			continue
		}
		overlap := false
		for _, span := range matched {
			overlap = overlap || position < span.end && position+len(phrase) > span.start
		}
		if overlap {
			continue
		}
		before, after := []rune(body[:position]), []rune(body[position+len(phrase):])
		quote := string(before[max(0, len(before)-20):]) + phrase + string(after[:min(20, len(after))])
		review.Issues = append(review.Issues, WritingIssue{
			Kind: "reading", Scope: "prose", Quote: quote,
			Explanation: "作者明确禁用表达「" + phrase + "」出现在本次新增正文中。",
			Suggestion:  "按当前人物的动作、说话目的或明确时间地点重新组织这句话，删去多余衔接，保留原有事实和人物声音。",
		})
		review.NeedsRevision = true
		matched = append(matched, struct{ start, end int }{position, position + len(phrase)})
		if len(matched) == 5 {
			break
		}
	}
}

// writingCraftStage maps normal application entry points onto the same built-in methods.
func writingCraftStage(action string) string {
	switch action {
	case "architecture", "outline":
		return "outline"
	case "continue", "rewrite":
		return "prose"
	case "review":
		return "review"
	default:
		return "composition"
	}
}

// voiceQuote matches spoken lines in Chinese corner, curly or plain double quotation marks.
var voiceQuote = regexp.MustCompile(`“[^”]*”|「[^」]*」|"[^"\n]*"`)

// voiceMarkers are narration signals of a character's own voice: exclamations, questions and modal particles.
const voiceMarkers = "！？!?啊呢吧嘛呀哦喽咧啦嘞呗哎"

// voiceProfile is a rough, explainable measure of narrative voice; it is not a quality score.
type voiceProfile struct {
	Lines          int     // spoken lines inside quotation marks
	DialogueMedian float64 // median characters per spoken line
	NarrationChars int     // non-space characters outside quotation marks
	NarrationVoice float64 // voice markers per thousand narration characters
}

// measureVoice counts spoken-line lengths and narration voice markers in one text.
func measureVoice(text string) voiceProfile {
	profile := voiceProfile{}
	lengths := []int{}
	for _, quote := range voiceQuote.FindAllString(text, -1) {
		lengths = append(lengths, charCount(quote)-2)
	}
	sort.Ints(lengths)
	if profile.Lines = len(lengths); profile.Lines > 0 {
		middle := profile.Lines / 2
		profile.DialogueMedian = float64(lengths[middle])
		if profile.Lines%2 == 0 {
			profile.DialogueMedian = float64(lengths[middle-1]+lengths[middle]) / 2
		}
	}
	markers := 0
	for _, char := range voiceQuote.ReplaceAllString(text, "") {
		if !unicode.IsSpace(char) {
			profile.NarrationChars++
			if strings.ContainsRune(voiceMarkers, char) {
				markers++
			}
		}
	}
	if profile.NarrationChars > 0 {
		profile.NarrationVoice = float64(markers) * 1000 / float64(profile.NarrationChars)
	}
	return profile
}

// voiceAnchorChapters picks up to four earlier saved chapters the author has worked on most:
// chapters marked final first, then those with the most saved versions, then the earliest.
// Only chapters before the target are used, so samples never reveal later events.
func voiceAnchorChapters(project *Project, target *Chapter) []*Chapter {
	if target == nil {
		return nil
	}
	anchors := []*Chapter{}
	for i := range project.Chapters {
		if chapter := &project.Chapters[i]; chapter.Number < target.Number && charCount(chapter.Body) >= 300 {
			anchors = append(anchors, chapter)
		}
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		a, b := anchors[i], anchors[j]
		if (a.Status == "final") != (b.Status == "final") {
			return a.Status == "final"
		}
		if len(a.Versions) != len(b.Versions) {
			return len(a.Versions) > len(b.Versions)
		}
		return a.Number < b.Number
	})
	return anchors[:min(4, len(anchors))]
}

// voiceExcerpt is one verbatim run of consecutive paragraphs from an anchor chapter.
type voiceExcerpt struct {
	Chapter    uint32
	Start, End int // paragraph range [Start, End)
	Text       string
	Dialogue   int // spoken lines of at least six characters
	Voice      int // voice markers outside quotation marks
}

// voiceExcerpts lists runs of consecutive paragraphs of 120 to 420 characters with their counts.
func voiceExcerpts(chapter *Chapter) []voiceExcerpt {
	paragraphs := []string{}
	for _, line := range strings.Split(chapter.Body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paragraphs = append(paragraphs, line)
		}
	}
	excerpts := []voiceExcerpt{}
	for start := range paragraphs {
		size := 0
		for end := start; end < len(paragraphs); end++ {
			if size += charCount(paragraphs[end]); size > 420 {
				break
			}
			if size >= 120 {
				text := strings.Join(paragraphs[start:end+1], "\n")
				excerpt := voiceExcerpt{Chapter: chapter.Number, Start: start, End: end + 1, Text: text}
				for _, quote := range voiceQuote.FindAllString(text, -1) {
					if charCount(quote)-2 >= 6 {
						excerpt.Dialogue++
					}
				}
				for _, char := range voiceQuote.ReplaceAllString(text, "") {
					if strings.ContainsRune(voiceMarkers, char) {
						excerpt.Voice++
					}
				}
				excerpts = append(excerpts, excerpt)
			}
		}
	}
	return excerpts
}

// pickVoiceExcerpt returns the first highest-scoring excerpt that reaches the minimum,
// avoids already chosen paragraphs and passes the chapter filter.
func pickVoiceExcerpt(excerpts, chosen []voiceExcerpt, minimum int, score func(voiceExcerpt) int, allow func(uint32) bool) (voiceExcerpt, bool) {
	best, found := voiceExcerpt{}, false
	for _, excerpt := range excerpts {
		overlap := false
		for _, taken := range chosen {
			overlap = overlap || taken.Chapter == excerpt.Chapter && excerpt.Start < taken.End && taken.Start < excerpt.End
		}
		if !overlap && allow(excerpt.Chapter) && score(excerpt) >= minimum && (!found || score(excerpt) > score(best)) {
			best, found = excerpt, true
		}
	}
	return best, found
}

// writingVoiceSamples picks a dialogue excerpt, a narration-voice excerpt and a second dialogue
// excerpt from another anchor chapter, keeping the total near 1300 characters.
func writingVoiceSamples(project *Project, target *Chapter) []voiceExcerpt {
	excerpts := []voiceExcerpt{}
	for _, chapter := range voiceAnchorChapters(project, target) {
		excerpts = append(excerpts, voiceExcerpts(chapter)...)
	}
	dialogue := func(excerpt voiceExcerpt) int { return excerpt.Dialogue }
	voice := func(excerpt voiceExcerpt) int { return excerpt.Voice }
	everyChapter := func(uint32) bool { return true }
	chosen, total := []voiceExcerpt{}, 0
	add := func(excerpt voiceExcerpt, ok bool) {
		if ok && total+charCount(excerpt.Text) <= 1300 {
			chosen, total = append(chosen, excerpt), total+charCount(excerpt.Text)
		}
	}
	add(pickVoiceExcerpt(excerpts, chosen, 3, dialogue, everyChapter))
	add(pickVoiceExcerpt(excerpts, chosen, 2, voice, everyChapter))
	if len(chosen) > 0 {
		first := chosen[0].Chapter
		add(pickVoiceExcerpt(excerpts, chosen, 3, dialogue, func(number uint32) bool { return number != first }))
	}
	return chosen
}

// writingVoiceBlock renders the samples for the prose stage with instructions to imitate voice, not plot.
func writingVoiceBlock(project *Project, target *Chapter) string {
	samples := writingVoiceSamples(project, target)
	if len(samples) == 0 {
		return ""
	}
	parts := []string{localText(project,
		"These verbatim excerpts come from earlier chapters the author has polished. Use them only to match the narrative voice: how much the viewpoint character thinks aloud, how long and how colloquial the spoken lines are, and the sentence rhythm. Do not reuse their events, jokes, comparisons or sentences.",
		"以下片段原样摘自作者修订过的前文，只用来对齐叙述声音：视角人物心里话和吐槽的分量、对白的长短和口气、句子的节奏。不要复用其中的情节、笑料、比喻和原句。")}
	for _, sample := range samples {
		parts = append(parts, fmt.Sprintf(localText(project, "[Chapter %d]", "[第%d章]"), sample.Chapter)+"\n"+sample.Text)
	}
	return "\n<voice_samples>\n" + strings.Join(parts, "\n\n") + "\n</voice_samples>"
}

// appendVoiceDriftIssues flags new prose whose narration voice or spoken-line length departs sharply
// from the anchor chapters. Only clipped dialogue requests the single unused revision: the narration
// count misses deadpan inner voice, so it stays an editing note. Neither blocks saving on its own.
func appendVoiceDriftIssues(project *Project, target *Chapter, draft string, revisions int, review *WritingReview) {
	anchors := voiceAnchorChapters(project, target)
	if review == nil || len(anchors) == 0 {
		return
	}
	bodies := []string{}
	for _, chapter := range anchors {
		bodies = append(bodies, chapter.Body)
	}
	base, candidate := measureVoice(strings.Join(bodies, "\n")), measureVoice(draft)
	if issue, found := narrationDriftIssue(project, base, candidate, draft); found {
		review.Issues = append(review.Issues, issue)
	}
	if issue, found := dialogueDriftIssue(project, base, candidate, draft); found {
		review.Issues = append(review.Issues, issue)
		if revisions == 0 {
			review.NeedsRevision = true
		}
	}
}

// narrationDriftIssue notes narration with far fewer exclamations, questions and modal particles
// than the anchor chapters, using a conservative threshold.
func narrationDriftIssue(project *Project, base, candidate voiceProfile, draft string) (WritingIssue, bool) {
	drift := base.NarrationChars >= 1500 && base.NarrationVoice >= 3 && candidate.NarrationChars >= 600 && candidate.NarrationVoice < base.NarrationVoice*0.3
	if !drift {
		return WritingIssue{}, false
	}
	flat := func(paragraph string) bool {
		return charCount(paragraph) >= 20 && !voiceQuote.MatchString(paragraph) && !strings.ContainsAny(paragraph, voiceMarkers)
	}
	return WritingIssue{Kind: "reading", Scope: "prose", Quote: voiceIssueQuote(draft, flat),
		Explanation: fmt.Sprintf(localText(project, "The narration has far fewer exclamations, rhetorical questions and modal particles than the earlier chapters (about %.1f per thousand characters against %.1f), so the viewpoint character's reactions may sound flatter than the established voice.", "叙述里的感叹、反问和语气词明显少于前文（每千字约 %.1f 处，前文样本章约 %.1f 处），视角人物的反应可能比前文平淡。"), candidate.NarrationVoice, base.NarrationVoice),
		Suggestion:  localText(project, "Following the voice samples, let the viewpoint character react in their own tone at key actions and lines, with surprise, worry or grumbling where it fits. Keep existing inner thoughts, events and dialogue content.", "参照文风样本，在关键动作和对话处让视角人物用自己的口气反应，该吃惊、着急或嘀咕的地方写出来；已有的心里话、情节和对白内容保留。")}, true
}

// dialogueDriftIssue flags spoken lines much shorter than in the anchor chapters, using a conservative threshold.
func dialogueDriftIssue(project *Project, base, candidate voiceProfile, draft string) (WritingIssue, bool) {
	drift := base.Lines >= 20 && candidate.Lines >= 8 && candidate.DialogueMedian <= 12 && candidate.DialogueMedian < base.DialogueMedian*0.6
	if !drift {
		return WritingIssue{}, false
	}
	clipped := func(paragraph string) bool {
		for _, quote := range voiceQuote.FindAllString(paragraph, -1) {
			if charCount(quote)-2 <= 8 {
				return true
			}
		}
		return false
	}
	return WritingIssue{Kind: "reading", Scope: "prose", Quote: voiceIssueQuote(draft, clipped),
		Explanation: fmt.Sprintf(localText(project, "Spoken lines are much shorter than in the earlier chapters (median about %.0f characters against %.0f), so the speakers' tone and mood may not come through.", "对白普遍比前文短（中位约 %.0f 字，前文样本章约 %.0f 字），人物的口气和情绪可能没有说出来。"), candidate.DialogueMedian, base.DialogueMedian),
		Suggestion:  localText(project, "In the key exchanges, let speakers finish their thoughts in their own temperament and mood, with complaints, jokes or verbal habits where they fit. Keep brief replies where they suit the moment, and keep the necessary information.", "在关键的几段对话里，让说话人按自己的性格和当下情绪把话说完整，合适处带上抱怨、玩笑或口头禅；合适的短促回应保留，必要信息不变。")}, true
}

// voiceIssueQuote returns a verbatim prefix of the first matching paragraph so the issue stays locatable.
func voiceIssueQuote(draft string, match func(string) bool) string {
	fallback := ""
	for _, line := range strings.Split(draft, "\n") {
		paragraph := strings.TrimSpace(line)
		if paragraph == "" {
			continue
		}
		if fallback == "" {
			fallback = paragraph
		}
		if match(paragraph) {
			fallback = paragraph
			break
		}
	}
	runes := []rune(fallback)
	return string(runes[:min(40, len(runes))])
}
