/** Exercise author-visible workflows through Playwright CLI against the real isolated application. */
async page => {
  const failures = [];
  page.on("pageerror", error => failures.push(String(error)));
  const mock = "__STUDIO_MOCK_URL__";
  const artifacts = __STUDIO_ARTIFACT_DIR_JSON__;
  const editorOnly = __STUDIO_EDITOR_ONLY__;
  const chapterLifecycleRegressions = __STUDIO_EDITOR_CASES__;
  const controlAudit = [];

  /** Assert an exact business expectation without weakening failed product checks. */
  function check(condition, detail) { if (!condition) throw new Error(detail); }

  /** Check actual field geometry across shared components, not their implementation classes alone. */
  async function regularFields(scope, section) {
    const controls = scope.locator('input:not([type="checkbox"]):not([type="radio"]):not([type="hidden"]):not([type="file"]), .qw-select-trigger');
    await controls.first().waitFor({ state: "visible" });
    const fields = await controls.evaluateAll(elements => elements.filter(e => e.getBoundingClientRect().height > 0).map(e => {
      const css = getComputedStyle(e);
      return { label: e.getAttribute("aria-label") || e.closest("label")?.textContent?.trim().slice(0, 80), height: e.getBoundingClientRect().height, font: css.fontSize, radius: css.borderRadius };
    }));
    check(fields.length > 0 && fields.every(f => f.height === 40 && f.font === "13px" && f.radius === "8px"), `${section} controls disagree: ${JSON.stringify(fields)}`);
    controlAudit.push({ section, fields });
  }

  /** Read real backend state through the application's public transport. */
  async function command(name, args = {}) {
    return page.evaluate(async ({ name, args }) => {
      const response = await fetch("/api/command", { method: "POST", headers: { "Content-Type": "application/json", "X-Quillweave-Client": "1" }, body: JSON.stringify({ command: name, args: { ...args, ui_language: "zh-CN" } }) });
      const result = await response.json();
      if (!result.ok) throw new Error(result.error);
      return result.result;
    }, { name, args });
  }

  /** Choose an option using the same searchable accessible control inside and outside native dialogs. */
  async function choose(scope, name, option) {
    await scope.getByRole("combobox", { name, exact: true }).click();
    await page.getByRole("option", { name: option, exact: true }).click();
  }

  /** Wait on the actual job state and corresponding rendered completed answer. */
  async function completed(jobId) {
    await page.locator(`[data-job-id="${jobId}"] .answer-heading`).getByText("已完成", { exact: true }).waitFor({ timeout: 20000 });
    const job = await command("get_job", { job_id: jobId });
    check(job.status === "done", `Job failed: ${job.status} ${job.error}`);
    await page.locator(".assistant-messages > .conversation").last().locator(".answer-heading").getByText("已完成", { exact: true }).waitFor();
    return job;
  }

  /** Send through the real composer and follow its accepted request to completion. */
  async function send(text) {
    const accepted = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai");
    await page.getByRole("textbox", { name: "写作请求", exact: true }).fill(text);
    await page.getByRole("textbox", { name: "写作请求", exact: true }).press("Enter");
    const result = await (await accepted).json();
    check(result.ok, result.error || "Send was rejected");
    return completed(result.result.id);
  }

  /** Confirm the exact rendered candidate and verify its persisted receipt. */
  async function confirmJob(job) {
    const card = page.locator(`[data-job-id="${job.id}"] .change-review`).first();
    await card.getByRole("button", { name: "确认写入", exact: true }).click();
    await card.getByText("已入库至故事设定", { exact: true }).waitFor();
    const saved = await command("get_project", { project_id: job.project_id });
    check(saved.studio.changesets.some(item => item.id === job.changesets[0].id && item.status === "applied"), "Rendered receipt lacks a persistent commit");
    return card;
  }

  /** Navigate within the four project areas without replacing the global conversation. */
  async function navigate(name) {
    await page.getByRole("navigation", { name: "作品工作区", exact: true }).getByRole("button", { name, exact: true }).click();
  }

  /** Check remote review conflicts, chapter selection ownership and context changes after a lost acknowledgement. */
  async function reviewAndSelectionRegressions(originalProjectId) {
    const original = await command("get_project", { project_id: originalProjectId });
    let project = await command("create_project", { title: "Review regression", genre: "Mystery", premise: "Two distinct chapter contexts" });
    for (const title of ["Selection chapter A", "Selection chapter B"]) {
      project = await command("new_chapter", { project_id: project.id, revision: project.revision, title, target_words: null });
      const chapter = project.chapters.at(-1);
      project = await command("save_chapter", { project_id: project.id, revision: project.revision, chapter_id: chapter.id, title, body: title === "Selection chapter A" ? "FIRST_CHAPTER_SELECTION" : "SECOND_CHAPTER_SELECTION", synopsis: "", status: "draft", target_words: null });
    }
    await page.evaluate(id => localStorage.setItem("quillweave-project", id), project.id);
    await page.reload();
    await page.locator(".project-name").filter({ hasText: "Review regression" }).click();
    const staged = await command("stage_changeset", { project_id: project.id, changeset: {
      source: "author", summary: "Keep local review edits", operations: [
        { id: "keep", target_kind: "entity", op_type: "upsert", title: "Retained edit", after_value: { name: "Retained edit", kind: "character", aliases: [], description: "Original candidate" } },
        { id: "skip", target_kind: "entity", op_type: "upsert", title: "Excluded edit", after_value: { name: "Excluded edit", kind: "character", aliases: [], description: "Remain excluded" } },
      ],
    } });
    const card = page.locator(`.workspace-content [data-changeset-id="${staged.id}"]`);
    await page.waitForFunction(id => {
      const element = document.querySelector(`.workspace-content [data-changeset-id="${id}"]`);
      return [...(element?.querySelectorAll("button") || [])].some(button => button.textContent.trim() === "确认写入" && !button.disabled);
    }, staged.id);
    const operation = card.locator(".change-review-operation").filter({ has: page.getByRole("checkbox", { name: "Retained edit", exact: true }) });
    const field = operation.locator("fieldset").filter({ has: page.locator("legend").getByText("描述", { exact: true }) });
    await field.locator("summary").click();
    const editor = field.locator("textarea");
    await editor.fill("LOCAL_REVIEW_EDIT");
    await card.getByRole("checkbox", { name: "Excluded edit", exact: true }).uncheck();
    await page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "get_project");
    check(await editor.inputValue() === "LOCAL_REVIEW_EDIT", "Unchanged polling discarded a review edit");
    let saved = await command("get_project", { project_id: project.id });
    const prior = saved.studio.changesets.find(item => item.id === staged.id);
    const changed = await command("preview_changeset", { project_id: project.id, changeset_id: staged.id, operations: prior.operations.map(op => ({ ...op, after_value: { ...op.after_value, description: "REMOTE_REVIEW_EDIT" } })) });
    await page.waitForResponse(async response => {
      if (!response.url().endsWith("/api/command") || response.request().postDataJSON()?.command !== "get_project") return false;
      const result = await response.json();
      return result.result?.studio?.changesets?.some(item => item.id === staged.id && item.review_token === changed.changeset.review_token);
    });
    await card.getByRole("alert").waitFor();
    check(await editor.inputValue() === "LOCAL_REVIEW_EDIT" && !await card.getByRole("checkbox", { name: "Excluded edit", exact: true }).isChecked(), "Remote preview replaced local fields or selection");
    check(await card.getByRole("button", { name: "确认写入", exact: true }).isDisabled(), "A conflicting local review could be confirmed directly");
    await card.getByRole("button", { name: "更新预览", exact: true }).click();
    await card.getByRole("button", { name: "确认写入", exact: true }).click();
    await card.getByText("已入库至故事设定", { exact: true }).waitFor();
    saved = await command("get_project", { project_id: project.id });
    check(saved.entities.length === 1 && saved.entities[0].description === "LOCAL_REVIEW_EDIT", "Re-reviewed local content was not the committed selection");
    check(saved.studio.changesets.find(item => item.id === staged.id).skipped_operations.length === 1, "Conflict resolution lost the skipped operation");

    const clean = await command("stage_changeset", { project_id: project.id, changeset: { source: "author", summary: "Clean review refresh", operations: [{ id: "clean", target_kind: "entity", op_type: "upsert", title: "Clean candidate", after_value: { name: "Clean candidate", kind: "character", aliases: [], description: "Original clean candidate" } }] } });
    const cleanCard = page.locator(`.workspace-content [data-changeset-id="${clean.id}"]`);
    await cleanCard.getByRole("button", { name: "确认写入", exact: true }).waitFor();
    const cleanPreview = await command("preview_changeset", { project_id: project.id, changeset_id: clean.id });
    await command("preview_changeset", { project_id: project.id, changeset_id: clean.id, operations: cleanPreview.changeset.operations.map(op => ({ ...op, after_value: { ...op.after_value, description: "REMOTE_CLEAN_PREVIEW" } })) });
    await cleanCard.locator(".change-review-comparison p").filter({ hasText: /^REMOTE_CLEAN_PREVIEW$/ }).waitFor();
    check(await cleanCard.getByRole("alert").count() === 0, "An untouched preview was incorrectly marked as conflicting");
    await cleanCard.getByRole("button", { name: "否决", exact: true }).click();

    await navigate("正文");
    const prose = page.getByRole("textbox", { name: "章节正文", exact: true });
    await prose.focus();
    await prose.press("Meta+A");
    await page.waitForFunction(() => window.getSelection()?.toString().includes("FIRST_CHAPTER_SELECTION"), undefined, { timeout: 15000 });
    await page.locator(".chapter-nav .chapter-item").filter({ hasText: "Selection chapter B" }).click();
    const prepared = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "prepare_ai");
    const composer = page.getByRole("textbox", { name: "写作请求", exact: true });
    await composer.fill("Check the current selection");
    await composer.fill("/context Check the current selection");
    await composer.press("Enter");
    const request = (await prepared).request().postDataJSON().args;
    check(request.chapter_id === project.chapters[1].id && !request.workspace_context.selected_text, "Chapter B carried chapter A's selection");
    await page.locator("dialog:modal header button").click();
    await page.locator(".chapter-nav .chapter-item").filter({ hasText: "Selection chapter A" }).click();
    check(!await page.evaluate(() => window.getSelection()?.toString().includes("FIRST_CHAPTER_SELECTION")), "Returning to a chapter revived its old selection");

    let lostJob = "";
    let lostRequest = "";
    const retryText = "Retry this request with the currently focused chapter";
    /** Lose one acknowledgement after the real backend accepts it, keeping the accepted request identity observable. */
    async function loseAcknowledgement(route) {
      const body = route.request().postDataJSON();
      if (!lostJob && body?.command === "start_ai" && body.args?.project_id === project.id && body.args?.instruction === retryText) {
        const result = await (await route.fetch()).json();
        check(result.ok, result.error || "The initial request was rejected");
        lostJob = result.result.id;
        lostRequest = body.args.request_id;
        await route.abort("failed");
      } else await route.continue();
    }
    await page.route("**/api/command", loseAcknowledgement);
    await composer.fill(retryText);
    await composer.press("Enter");
    await page.getByRole("alert").last().waitFor();
    let acknowledged;
    const completionDeadline = Date.now() + 15000;
    do {
      acknowledged = await command("get_job", { job_id: lostJob });
      if (acknowledged.status !== "running") break;
      await page.waitForTimeout(100);
    } while (Date.now() < completionDeadline);
    check(acknowledged?.status === "done", `The accepted task did not finish: ${acknowledged?.status} ${acknowledged?.error}`);
    await page.unroute("**/api/command", loseAcknowledgement);
    const completedSession = await command("get_conversation", { project_id: project.id, session_id: acknowledged.session_id });
    check(completedSession.turns.every(turn => turn.status !== "running"), `A completed task left a running turn: ${JSON.stringify(completedSession.turns.map(({ role, status, job_id, request_id }) => ({ role, status, job_id, request_id })))}`);
    await page.locator(".chapter-nav .chapter-item").filter({ hasText: "Selection chapter B" }).click();
    const retriedRequest = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai");
    let retried;
    try {
      retried = await send(retryText);
    } catch (cause) {
      const latest = await command("get_conversation", { project_id: project.id, session_id: acknowledged.session_id });
      throw new Error(`${String(cause)}; retry state ${JSON.stringify(latest.turns.map(({ role, status, job_id, request_id }) => ({ role, status, job_id, request_id })))}`);
    }
    const retryArgs = (await retriedRequest).request().postDataJSON().args;
    check(retried.id !== lostJob && retryArgs.request_id !== lostRequest && retried.chapter_id === project.chapters[1].id, "A changed chapter reused an earlier context's accepted request");

    saved = await command("get_project", { project_id: project.id });
    await command("delete_project", { project_id: project.id, revision: saved.revision });
    await page.evaluate(id => localStorage.setItem("quillweave-project", id), originalProjectId);
    await page.reload();
    await page.locator(".project-name").filter({ hasText: original.title }).click();
    controlAudit.push({ section: "review conflicts and chapter selection", localEditsPreserved: true, selectionPreserved: true, cleanRefresh: true, staleSelectionCleared: true, changedContextUsesNewRequest: true });
  }

  /** Exercise an external candidate, map references and chapter-two continuation through visible controls. */
  async function mapAndSecondChapter(projectId) {
    const external = await command("stage_changeset", { project_id: projectId, changeset: {
      source: "cli", summary: "外部 Agent 待审阅资料", operations: [
        { id: "character", target_kind: "entity", op_type: "upsert", title: "林舟", after_value: { id: "external-lin", name: "林舟", kind: "character", aliases: [], description: "交出钥匙的人" } },
        { id: "skipped", target_kind: "decision", op_type: "upsert", title: "待取消的决定", after_value: { title: "待取消的决定", content: "这项候选应保留为未采纳" } },
      ],
    } });
    await navigate("作品");
    const externalCard = page.locator(`.workspace-content [data-changeset-id="${external.id}"]`);
    await externalCard.waitFor({ timeout: 10000 });
    await externalCard.getByRole("checkbox").nth(1).uncheck();
    await externalCard.getByRole("button", { name: "更新预览", exact: true }).click();
    await externalCard.getByRole("button", { name: "确认写入", exact: true }).click();
    await externalCard.getByText("已入库至故事设定", { exact: true }).waitFor();
    let saved = await command("get_project", { project_id: projectId });
    check(saved.entities.some(item => item.name === "林舟") && !saved.studio.decisions.some(item => item.title === "待取消的决定"), "Partial confirmation saved an excluded operation");
    check(saved.studio.changesets.find(item => item.id === external.id).skipped_operations.length === 1, "The excluded candidate lost its review history");

    await navigate("设定");
    await page.getByRole("tab", { name: "有效故事资料", exact: true }).click();
    const knowledge = page.getByRole("region", { name: "有效故事资料", exact: true });
    await knowledge.getByText(/^当前召回/).waitFor();
    await knowledge.getByRole("textbox", { name: "搜索人物、地点或事件", exact: true }).fill("林舟");
    await knowledge.getByRole("button", { name: "搜索", exact: true }).click();
    await knowledge.locator(".knowledge-records").getByText(/铜钥匙/).first().waitFor();
    check((await knowledge.innerText()).includes("来源候选"), "The unified knowledge view omitted its evidence boundary");
    await page.getByRole("tab", { name: "世界地图", exact: true }).click();
    for (const name of ["北塔", "旧驿站"]) {
      await page.getByRole("button", { name: "添加地点", exact: true }).click();
      const form = page.getByRole("dialog", { name: "地点", exact: true });
      await form.getByRole("textbox", { name: "名称", exact: true }).fill(name);
      await form.getByRole("textbox", { name: "描述", exact: true }).fill(`${name}与铜钥匙的转移相关。`);
      await form.locator("summary").filter({ hasText: "关联章节" }).click();
      await form.getByRole("checkbox", { name: "第一章：铜钥匙", exact: true }).check();
      await form.getByRole("button", { name: "预览修改", exact: true }).click();
      await form.waitFor({ state: "detached" });
      check(!((await command("get_project", { project_id: projectId })).studio.locations || []).some(item => item.name === name), "A location was saved before confirmation");
      const card = page.locator(".world-map .change-review").filter({ has: page.getByRole("button", { name: "确认写入", exact: true }) });
      await card.getByRole("button", { name: "确认写入", exact: true }).click();
      await page.locator(".world-map-canvas").getByRole("button", { name, exact: true }).waitFor();
    }
    await page.getByRole("button", { name: "添加路线", exact: true }).click();
    const route = page.getByRole("dialog", { name: "移动路线", exact: true });
    await route.getByRole("spinbutton", { name: "移动时间（小时）", exact: true }).fill("3.5");
    await route.getByRole("button", { name: "预览修改", exact: true }).click();
    await route.waitFor({ state: "detached" });
    const routeCard = page.locator(".world-map .change-review").filter({ has: page.getByRole("button", { name: "确认写入", exact: true }) });
    await routeCard.getByRole("button", { name: "确认写入", exact: true }).click();
    await page.locator(".world-map-route").waitFor();
    saved = await command("get_project", { project_id: projectId });
    check(saved.studio.locations.length === 2 && saved.studio.routes[0].travel_hours === 3.5, "Map geometry or travel time did not persist");
    const locationNode = page.locator(".world-map-canvas").getByRole("button", { name: "北塔", exact: true });
    await locationNode.focus();
    await locationNode.press("Enter");
    await page.locator(".map-location-detail").getByRole("heading", { name: "北塔", exact: true }).waitFor();
    await page.locator(".workspace-content").evaluate(element => { element.scrollTop = 0; });
    await page.screenshot({ path: `${artifacts}/world-map.png` });
    await page.locator(".map-location-detail").getByRole("button", { name: "第一章：铜钥匙", exact: true }).click();
    await page.getByRole("textbox", { name: "章节正文", exact: true }).waitFor();

    await navigate("大纲");
    await page.locator(".plan-directory").getByRole("button", { name: /第一卷：北塔/ }).click();
    await page.locator(".plan-card").filter({ has: page.getByRole("heading", { name: "第一卷：北塔", exact: true }) }).getByRole("button", { name: "新增规划", exact: true }).click();
    const plan = page.getByRole("dialog", { name: "新增规划", exact: true });
    await choose(plan, "层级", "章节");
    await plan.getByRole("textbox", { name: "标题", exact: true }).fill("第二章：旧驿站");
    await plan.getByRole("spinbutton", { name: "起始章节", exact: true }).fill("2");
    await plan.getByRole("spinbutton", { name: "结束章节", exact: true }).fill("2");
    await plan.getByRole("textbox", { name: "目标", exact: true }).fill("携带铜钥匙前往旧驿站");
    await plan.getByRole("textbox", { name: "选择", exact: true }).fill("先向林舟核对钥匙来历，再决定前往旧驿站");
    await plan.getByRole("button", { name: "保存", exact: true }).click();
    await plan.waitFor({ state: "detached" });
    check((await page.locator(".plan-card").innerText()).includes("先向林舟核对钥匙来历"), "The plan detail omitted the character's action");
    check((await page.locator(".plan-card").innerText()).includes("目标字数未设定"), "An unset target was displayed as zero written characters");
    const planLayout = await page.locator(".plan-workspace").evaluate(element => {
      const directory = element.querySelector(".plan-directory").getBoundingClientRect();
      const detail = element.querySelector(".plan-card").getBoundingClientRect();
      return { directory: directory.toJSON(), detail: detail.toJSON() };
    });
    check(planLayout.detail.x >= planLayout.directory.right || planLayout.detail.y >= planLayout.directory.bottom, "The responsive plan directory overlaps its active detail");
    await page.screenshot({ path: `${artifacts}/plans.png`, fullPage: true });
    await page.getByRole("button", { name: "建立章节草稿", exact: true }).click();
    await navigate("大纲");
    await page.locator(".plan-card").filter({ has: page.getByRole("heading", { name: "第二章：旧驿站", exact: true }) }).getByRole("button", { name: "进入章节", exact: true }).click();
    const prose = await send("为第二章生成正文，沿用已经确认的钥匙转移事实。");
    check(JSON.stringify(prose.prompt).includes("铜钥匙转移"), "The second chapter request omitted the confirmed chapter-one memory");
    check(prose.writing?.review?.structured && prose.writing.mode === "changeset" && prose.writing.stages.length === 1, "An Agent chapter changeset bypassed its review stage");
    await confirmJob(prose);
    saved = await command("get_project", { project_id: projectId });
    check(saved.chapters.length === 2 && saved.chapters[1].body.includes("沿山路走向旧驿站"), "Confirmed prose did not reach chapter two");
    check(saved.chapters[0].body.includes("他再也想不起母亲的声音"), "Chapter-two confirmation overwrote chapter one");
    const workflow = await send("/write 写作流程验收，承接第二章结尾写一个具体的日常场景。");
    check(workflow.action === "continue" && workflow.writing?.review?.structured && workflow.writing.stages.length === 3 && workflow.writing.composition.scene_cards.length > 0, "The write command did not execute composition, drafting and review");
    const workflowCard = page.locator(`[data-job-id="${workflow.id}"]`);
    check(!(await workflowCard.locator(".chat-activity").innerText()).includes('"scene_cards"'), "Structured scene payload leaked into prose messages");
    saved = await command("get_project", { project_id: projectId });
    check(workflow.writing.saved && saved.chapters[1].body.includes("两人约好天亮"), "The explicit writing command did not save its checked result");
    await workflowCard.getByText(/实际阶段输入/).click();
    await workflowCard.getByRole("button", { name: /3 · 审阅候选正文/ }).click();
    const stageContext = page.getByRole("dialog", { name: "请求上下文", exact: true });
    await stageContext.waitFor();
    check((await stageContext.innerText()).includes("必需材料"), "The review context did not identify required evidence");
    await stageContext.getByRole("button", { name: "关闭对话框", exact: true }).click();
    await page.screenshot({ path: `${artifacts}/writing-workflow.png`, fullPage: true });
    const recomposedStart = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai" && response.request().postDataJSON()?.args?.intent_hint === "writing_recompose");
    await workflowCard.getByRole("button", { name: "重新构思", exact: true }).click();
    const recomposedPayload = await (await recomposedStart).json();
    check(recomposedPayload.ok, recomposedPayload.error || "Recomposition was rejected");
    const recomposed = await completed(recomposedPayload.result.id);
    check(recomposed.action === "rewrite" && recomposed.writing?.composition && recomposed.writing.stages.length === 3 && recomposed.writing.stages[0].name === "composing", "Recomposition reused the old brief or skipped its actual planning request");
    const compositionCard = page.locator(`[data-job-id="${recomposed.id}"]`);
    await compositionCard.getByText("采用的局部构思", { exact: true }).click();
    check((await compositionCard.innerText()).includes("岑月与林舟先核对旧信"), "The actual composition was not visible to the author");
    const unaccepted = await command("get_project", { project_id: projectId });
    check(unaccepted.chapters[1].body === saved.chapters[1].body, "A recomposition follow-up overwrote saved prose before acceptance");
    const failedStart = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai");
    await page.getByRole("textbox", { name: "写作请求", exact: true }).fill("中断代理恢复验收，继续讨论下一步。");
    await page.getByRole("textbox", { name: "写作请求", exact: true }).press("Enter");
    const failed = (await (await failedStart).json()).result;
    await page.locator(`[data-job-id="${failed.id}"]`).getByRole("button", { name: "继续任务", exact: true }).waitFor();
    const resumedStart = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai" && response.request().postDataJSON()?.args?.resume_job_id === failed.id);
    await page.locator(`[data-job-id="${failed.id}"]`).getByRole("button", { name: "继续任务", exact: true }).click();
    const resumedPayload = await (await resumedStart).json();
    check(resumedPayload.ok, resumedPayload.error || "Agent recovery was rejected");
    const resumed = await completed(resumedPayload.result.id);
    check(resumed.session_id === failed.session_id && JSON.stringify(resumed.prompt).includes(`Resume task ${failed.id}`), "Desktop recovery lost the task's conversation or saved context");
    const selected = await command("get_conversation", { project_id: projectId, session_id: resumed.session_id });
    for (let index = 0; index < 105; index++) {
      await command("create_conversation", { project_id: projectId, chapter_id: "", title: `旧会话 ${index}` });
    }
    const draft = "翻页切换后仍保留的草稿";
    await page.getByRole("textbox", { name: "写作请求", exact: true }).fill(draft);
    await page.locator(".conversation-tools > button").first().click();
    const conversations = page.getByRole("dialog", { name: "会话", exact: true });
    await conversations.getByRole("button", { name: "加载更早的会话", exact: true }).click();
    await conversations.locator(".history-row").filter({ has: page.getByText("旧会话 0", { exact: true }) }).click();
    await conversations.waitFor({ state: "detached" });
    check(await page.getByRole("textbox", { name: "写作请求", exact: true }).inputValue() === "", "Selecting an older empty conversation copied another session's draft");
    await page.locator(".conversation-tools > button").first().click();
    await conversations.getByRole("button", { name: "加载更早的会话", exact: true }).click();
    await conversations.locator(".history-row").filter({ has: page.getByText(selected.title, { exact: true }) }).click();
    await conversations.waitFor({ state: "detached" });
    check(await page.getByRole("textbox", { name: "写作请求", exact: true }).inputValue() === draft, "The selected conversation outside page one lost its draft or identity");
  }

  /** Wait for a completed browser paint after resizing before measuring or capturing the layout. */
  async function viewport(width, height) {
    await page.setViewportSize({ width, height });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  }

  /** Verify direct key editing against real settings commands using only disposable fake credentials. */
  async function secretInputs() {
    const checks = [];
    const mask = "••••••••";
    const profile = (await command("get_model_settings")).settings.profiles[0];
    const profileId = profile.id;
    const modelName = "API 密钥";
    const firstKey = "acceptance-only-model-first";
    const secondKey = "acceptance-only-model-second";

    /** Wait for the controlled field to reflect the server's presence flag without containing a secret. */
    async function field(name, configured) {
      await page.waitForFunction(({ name, placeholder }) => {
        const input = [...document.querySelectorAll("input")].find(input => input.getAttribute("aria-label") === name);
        return input?.type === "password" && input.value === "" && (input.getAttribute("placeholder") || "") === placeholder;
      }, { name, placeholder: configured ? mask : "" });
      return page.getByLabel(name, { exact: true });
    }

    /** Observe the actual save envelope and response rather than treating a click or empty toast as proof. */
    async function save(commandName, buttonName, success = true) {
      const pending = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === commandName);
      await page.getByRole("button", { name: buttonName, exact: true }).click();
      const response = await pending;
      const payload = await response.json();
      check(payload.ok === success, "Credential save returned an unexpected outcome");
      return { args: response.request().postDataJSON().args, result: payload.result };
    }

    let model = await field(modelName, false);
    check(await page.getByRole("combobox", { name: "API 密钥操作", exact: true }).count() === 0, "An operation menu still gates key entry");
    checks.push("unconfigured model: blank password field, no operation menu");

    await model.fill(firstKey);
    check(await model.getAttribute("type") === "password", "Typed keys became readable text");
    let saved = await save("save_model_settings", "保存模型设置");
    check(saved.args.key_updates[profileId]?.action === "replace" && saved.args.key_updates[profileId].value === firstKey, "Direct typing did not submit the exact replacement");
    check(saved.result.key_status[profileId] && !JSON.stringify(saved.result).includes(firstKey), "A stored key was returned to the UI");
    model = await field(modelName, true);
    checks.push("new key: exact replace request, secret omitted from response, masked after save");

    await page.getByRole("button", { name: "我的作品", exact: true }).click();
    await page.locator(".sidebar-bottom").getByRole("button", { name: "设置", exact: true }).click();
    model = await field(modelName, true);
    check(!(await page.locator("body").textContent()).includes(firstKey), "Reopening the form exposed a key");
    checks.push("reopened configured field: mask only, no secret readback");

    await page.getByLabel("配置名称", { exact: true }).fill(profile.name + " · 密钥验收");
    saved = await save("save_model_settings", "保存模型设置");
    check(!saved.args.key_updates[profileId] && saved.result.key_status[profileId], "An untouched credential was overwritten while saving another field");
    check(!JSON.stringify(saved.args).includes(mask), "The mask was submitted as credential material");
    model = await field(modelName, true);
    checks.push("unrelated model edit: existing key preserved, mask never submitted");

    await model.fill(secondKey);
    /** Fail only one test-owned save response so retry behavior is exercised without mutating the real backend. */
    const failSave = async route => {
      if (route.request().postDataJSON()?.command === "save_model_settings")
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: false, error: "验收夹具：保存暂时不可用" }) });
      else await route.continue();
    };
    await page.route("**/api/command", failSave);
    await save("save_model_settings", "保存模型设置", false);
    await page.getByRole("alert").filter({ hasText: "验收夹具：保存暂时不可用" }).waitFor();
    check(await model.inputValue() === secondKey && await model.getAttribute("type") === "password", "A failed save lost or revealed the draft key");
    await page.unroute("**/api/command", failSave);
    saved = await save("save_model_settings", "保存模型设置");
    check(saved.args.key_updates[profileId].value === secondKey && saved.result.key_status[profileId], "Retry did not save the retained replacement draft");
    model = await field(modelName, true);
    checks.push("failed save: masked draft retained and exact retry succeeds");

    await model.focus();
    await model.press("Tab");
    const clearModel = page.getByRole("button", { name: "清除密钥 · API 密钥", exact: true });
    check(await clearModel.evaluate(button => button === document.activeElement), "Clear is missing from the keyboard sequence");
    check((await clearModel.boundingBox()).height === 40, "The clear action uses a different control density");
    await clearModel.press("Space");
    model = await field(modelName, false);
    check(await model.evaluate(input => input === document.activeElement), "Clear did not return focus to the input");
    saved = await save("save_model_settings", "保存模型设置");
    check(saved.args.key_updates[profileId].action === "clear" && !saved.result.key_status[profileId], "Clear did not remove the stored model credential");
    await field(modelName, false);
    checks.push("keyboard clear: explicit clear request, empty field, focus restored");

    check(await page.getByLabel("Hindsight 密钥", { exact: true }).count() === 0, "Retired external-memory credentials remain visible");
    check(!(await command("get_integrations")).settings.hindsight_url, "Retired endpoint survived migration");
    checks.push("retired external-memory controls and destination removed");

    await viewport(860, 640);
    await model.scrollIntoViewIfNeeded();
    check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "Direct credential controls overflow the minimum window");
    const geometry = await page.locator(".secret-input-row > input").evaluateAll(inputs => inputs.map(input => ({ height:input.getBoundingClientRect().height, font:getComputedStyle(input).fontSize, radius:getComputedStyle(input).borderRadius })));
    check(geometry.length === 1 && geometry.every(input => input.height === 40 && input.font === "13px" && input.radius === "8px"), "Secret inputs diverged from the shared field geometry");
    checks.push("minimum window and geometry: no overflow, 40px / 13px / 8px");
    await viewport(1440, 900);
    controlAudit.push({ section: "direct secret inputs", checks, geometry });
  }

/** Recover legacy settings and a stored text-field memory through a disposable imported work and real review controls. */
  async function legacyGoalSave() {
    const original = await command("create_project", { title: "旧版保存用例来源", genre: "玄幻", premise: "守墓人的日常" });
    const legacyStyle = "去AI痕迹精修规范（Humanizer准则）\n1. 保留人物独立声音。\n2. 用具体行动承接情绪。";
    original.title = "旧版创作目标保存验收";
    original.writing_style = legacyStyle;
    original.narrative_pov = "third_limited";
    original.style = "慢节奏日常与克制幽默。";
    const memoryText = "【重构原则】误会经过可见行动与转述。\n【收尾局面】第一单元结束时仍保留两笔人情债。";
    original.studio.changesets = [{ id: "legacy-memory-review", project_id: original.id, source: "agent", status: "staged", commit_policy: "review", summary: "旧格式第一单元方案", operations: [{ id: "legacy-memory-op", target_kind: "memory", target_id: "", op_type: "upsert", after_value: { kind: "setting", text: memoryText, title: "第一单元重构脊与收尾局面" } }] }];
    const imported = await command("import_archive", { content: JSON.stringify({ format: "quillweave-1", project: original }) });
    await page.getByRole("button", { name: "我的作品", exact: true }).click();
    await page.reload();
    await page.getByRole("button", { name: "我的作品", exact: true }).click();
    await page.locator(".project-card-main, .project-name").filter({ hasText: imported.title }).click();
    await page.getByRole("button", { name: "创作目标", exact: true }).click();
    const goals = page.getByRole("dialog", { name: "创作目标", exact: true });
    const styleControl = goals.locator(".writing-parameters .qw-select-trigger").nth(0);
    await page.waitForFunction(() => document.querySelector(".writing-parameters")?.textContent.includes("旧版风格指令"));
    check((await styleControl.innerText()).includes("自定义"), "A legacy instruction block was still displayed as a preset ID");
    check((await goals.locator(".writing-parameters .qw-select-trigger").nth(1).innerText()).includes("有限视角"), "The legacy viewpoint enum was not displayed normally");
    const reading = "守墓事务、同门相处和生存选择，保留误会式幽默。";
    await goals.getByRole("textbox", { name: "本书持续提供的阅读体验", exact: true }).fill(reading);
    const response = page.waitForResponse(r => r.url().endsWith("/api/command") && r.request().postDataJSON()?.command === "save_studio");
    await goals.getByRole("button", { name: "保存创作目标", exact: true }).click();
    const receipt = await (await response).json();
    check(receipt.ok, receipt.error || "Legacy goals could not be saved");
    await goals.waitFor({ state: "detached" });
    const saved = await command("get_project", { project_id: imported.id });
    check(saved.writing_style === "custom" && saved.narrative_pov === "Third-person limited" && saved.style.includes(legacyStyle) && saved.studio.narrative_focus === reading, "The repaired manual save lost instructions or edited content");
    await page.getByRole("button", { name: "创作目标", exact: true }).click();
    await goals.waitFor();
    check(await goals.locator("textarea").evaluateAll((elements, text) => elements.some(element => element.value.includes(text)), legacyStyle), "Reopening goals lost the migrated style notes");
    await page.screenshot({ path: `${artifacts}/legacy-goals-saved.png`, fullPage: true });
    await goals.getByRole("button", { name: "取消", exact: true }).click();
    const review = page.locator('.workspace-content [data-changeset-id="legacy-memory-review"]');
    await review.getByRole("button", { name: "确认写入", exact: true }).waitFor();
    await page.waitForFunction(() => !document.querySelector('.workspace-content [data-changeset-id="legacy-memory-review"] button.primary')?.disabled);
    check(!(await review.innerText()).includes("Invalid or duplicate memory"), "The legacy memory still failed preview validation");
    const recovered = await command("get_project", { project_id: imported.id });
    const memoryOp = recovered.studio.changesets.find(item => item.id === "legacy-memory-review").operations[0];
    check(memoryOp.after_value.content === memoryText && !Object.hasOwn(memoryOp.after_value, "text") && recovered.studio.memories.length === 0, "Preview lost legacy text or adopted the candidate before confirmation");
    const typeField = review.getByRole("group", { name: "类型", exact: true });
    await typeField.locator("summary").click();
    await choose(typeField, "编辑类型", "未来规划");
    check(await review.getByRole("button", { name: "确认写入", exact: true }).isDisabled(), "Changing memory type did not invalidate the frozen review");
    await review.getByRole("button", { name: "更新预览", exact: true }).click();
    await review.getByRole("button", { name: "确认写入", exact: true }).click();
    await review.getByText("已入库至故事设定", { exact: true }).waitFor();
    const adopted = await command("get_project", { project_id: imported.id });
    check(adopted.studio.memories.length === 1 && adopted.studio.memories[0].kind === "plan" && adopted.studio.memories[0].content === memoryText, "The explicit future-plan adoption lost its type or text");
    await page.screenshot({ path: `${artifacts}/legacy-memory-recovered.png`, fullPage: true });
    const outlined = await command("save_plan", { project_id: imported.id, revision: adopted.revision, plan: { id: "ui-auto-chapter-plan", level: "chapter", title: "第一章：旧信与风", start_chapter: 1, end_chapter: 1, status: "confirmed", goal: "核对旧信的日期", choice: "约好天亮再问守门人", characters: [], clues: [] } });
    check(outlined.chapters.length === 0, "The automatic writing fixture already had a chapter");
    const automatic = await send("/write CHAT_AUTO_CHAPTER 根据章纲写第一章，自动创建并保存");
    const automaticallySaved = await command("get_project", { project_id: imported.id });
    check(automatic.writing?.saved && automaticallySaved.chapters.length === 1 && automaticallySaved.chapters[0].body.includes("岑月先把行囊里的旧信摊开"), "Writing from the outline required a manually created chapter or lost its prose");
    await page.locator(`[data-job-id="${automatic.id}"] .chat-tool-card`).filter({ hasText: "创作章节正文" }).waitFor();
    await page.locator(`[data-job-id="${automatic.id}"] .chat-tool-card`).filter({ hasText: "创作章节正文" }).locator("summary").click();
    await page.screenshot({ path: `${artifacts}/automatic-chapter-saved.png`, fullPage: true });
    controlAudit.push({ section: "outline writing", createdAutomatically: true, proseSaved: true, toolReceiptVisible: true });
    const originalWork = await command("get_project", { project_id: projectId });
    await page.getByRole("button", { name: "我的作品", exact: true }).click();
    await page.locator(".project-card-main, .project-name").filter({ hasText: originalWork.title }).first().click();
    await page.getByRole("button", { name: "创作目标", exact: true }).waitFor();
    await command("delete_project", { project_id: imported.id, revision: automaticallySaved.revision });
    await command("delete_project", { project_id: original.id, revision: original.revision });
    controlAudit.push({ section: "legacy writing settings", manualSave: true, viewpointNormalized: true, instructionsRetained: true, reopened: true });
    controlAudit.push({ section: "legacy memory candidate", previewRecovered: true, futurePlanSelected: true, explicitAdoption: true, contentRetained: true });
  }

  if (editorOnly) {
    await page.setViewportSize({ width: 1440, height: 900 });
    await choose(page, "Interface language", "中文");
    const original = await command("create_project", { title: "Editor regression original", genre: "Mystery", premise: "Isolated editor acceptance" });
    await chapterLifecycleRegressions({ page, command, check, navigate, originalProjectId: original.id, artifacts });
    check(failures.length === 0, failures.join("\n"));
    return { result: "PASS: chapter draft, conflict, save ordering and deletion recovery" };
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await choose(page, "Interface language", "中文");
  await page.getByRole("button", { name: "开始新故事", exact: true }).click();
  await page.getByRole("textbox", { name: "暂定名称（可选）", exact: true }).fill("北塔借忆人 · 完整验收");
  await page.getByRole("button", { name: "开始找灵感", exact: true }).click();
  await page.getByRole("navigation", { name: "作品工作区", exact: true }).waitFor();
  check(await page.getByRole("navigation", { name: "作品工作区", exact: true }).getByRole("button").count() === 4, "The four primary project destinations are missing");
  let project = (await command("list_projects"))[0];
  const projectId = project.id;
  const input = page.getByRole("textbox", { name: "写作请求", exact: true });

  await input.fill("正在组字的创意");
  await input.evaluate(element => element.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, isComposing: true })));
  check((await command("get_runs", { project_id: projectId })).length === 0, "IME submitted while composing");
  await input.press("Shift+Enter");
  check((await input.inputValue()).endsWith("\n"), "Shift+Enter did not insert a newline");
  const first = await send("我想写一个靠记忆交易生存的长篇故事。");
  check((await input.inputValue()) === "", "Successful send did not clear the composer");
  check(await page.locator("dialog:modal").count() === 0, "Discussion opened an unsolicited modal");
  check(await page.getByRole("button", { name: "根据讨论生成大纲", exact: true }).count() === 0, "Discussion prematurely pushed a complete outline");
  const firstCard = page.locator(`[data-job-id="${first.id}"] .change-review`);
  check((await command("get_project", { project_id: projectId })).studio.decisions.length === 0, "Generation saved a decision before preview confirmation");
  const editContent = firstCard.getByRole("textbox", { name: "编辑内容", exact: true, includeHidden: true });
  await editContent.locator("xpath=ancestor::details").locator("summary").click();
  await editContent.fill("每次交易都要放弃一段重要且真实的记忆。");
  check(await firstCard.getByRole("button", { name: "确认写入", exact: true }).isDisabled(), "An edited preview remained confirmable without re-review");
  await firstCard.getByRole("button", { name: "更新预览", exact: true }).click();
  await confirmJob(first);
  project = await command("get_project", { project_id: projectId });
  check(project.studio.decisions.length === 1 && project.studio.decisions[0].content.includes("重要且真实"), "Edited candidate did not become shared author knowledge");

  await input.fill("跨页面保留的讨论草稿");
  await navigate("设定");
  await page.locator(".knowledge-graph").waitFor();
  const graphBox = await page.locator(".knowledge-graph").boundingBox();
  check(graphBox && graphBox.width >= 500, `Knowledge graph collapsed inside the shared workspace: ${JSON.stringify(graphBox)}`);
  await navigate("大纲");
  await navigate("正文");
  await navigate("作品");
  await page.locator(`[data-job-id="${first.id}"] .answer-heading`).getByText("已完成", { exact: true }).waitFor();
  check((await input.inputValue()) === "跨页面保留的讨论草稿", "Page switching replaced the global conversation draft");
  check(await page.getByRole("button", { name: "写作方法", exact: true }).count() === 0 && await page.getByRole("combobox", { name: "AI 任务", exact: true }).count() === 0, "Obsolete method or chat-mode navigation remains");

  await page.getByRole("button", { name: "重命名会话", exact: true }).click();
  await page.getByRole("textbox", { name: "会话名称", exact: true }).fill("北塔核心创意");
  await page.getByRole("dialog", { name: "重命名会话", exact: true }).getByRole("button", { name: "保存", exact: true }).click();
  await page.getByRole("button", { name: "北塔核心创意", exact: true }).waitFor();
  await page.getByRole("button", { name: "编辑并分支", exact: true }).click();
  await page.waitForFunction(() => document.querySelector('textarea[aria-label="写作请求"]')?.value.includes("记忆交易"));
  check((await input.inputValue()).includes("记忆交易"), "Rewind did not restore the selected message");
  const sessions = await command("list_conversations", { project_id: projectId, chapter_id: "" });
  check(sessions.length === 2 && sessions.some(session => session.parent_id), "Rewind did not retain the original branch");
  await send("换一个主角角度继续完善记忆交易。请先讨论冲突。");
  check((await command("get_conversation", { project_id: projectId, session_id: first.session_id })).turns.length === 2, "A branch mutated its original conversation");

  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route("**/api/command", async route => {
    if (route.request().postDataJSON()?.command === "start_ai") { await gate; await route.abort("failed"); }
    else await route.continue();
  });
  await input.fill("这一条用于验证失败恢复。");
  await input.press("Enter");
  await input.fill("失败期间输入的新草稿必须保留。");
  release();
  await page.locator(".assistant-composer").getByRole("alert").waitFor();
  check((await input.inputValue()) === "失败期间输入的新草稿必须保留。", "Failure overwrote a newer draft");
  await page.unroute("**/api/command");

  await page.getByRole("button", { name: "创作目标", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "创作目标", exact: true });
  await regularFields(dialog, "story goals");
  check(await dialog.locator(".qw-required").count() >= 1, "Required fields lost their visible indication");
  const formSelect = dialog.getByRole("combobox", { name: "作品形态", exact: true });
  await formSelect.press("ArrowDown");
  const optionSearch = page.getByRole("textbox", { name: "搜索选项", exact: true });
  await optionSearch.fill("传统小说");
  await optionSearch.press("Escape");
  check(await dialog.isVisible() && await formSelect.evaluate(e => document.activeElement === e), "Select Escape closed the dialog or lost focus");
  await formSelect.press("ArrowDown");
  await page.getByRole("textbox", { name: "搜索选项", exact: true }).press("Tab");
  check(await page.getByRole("listbox").count() === 0 && await dialog.evaluate(e => e.contains(document.activeElement)), "Tab left an open menu or escaped the modal focus scope");
  check(await dialog.getByRole("combobox", { name: "阶段", exact: true }).evaluate(e => document.activeElement === e), "Select Tab did not continue to the next form field");
  await formSelect.press("ArrowDown");
  await page.getByRole("textbox", { name: "搜索选项", exact: true }).press("Shift+Tab");
  check(await dialog.getByRole("combobox", { name: "作品语言", exact: true }).evaluate(e => document.activeElement === e), "Select Shift+Tab did not return to the previous form field");
  const dialogBox = await dialog.boundingBox();
  await page.mouse.click(dialogBox.x + .5, dialogBox.y + 45);
  check(await dialog.isVisible(), "An internal border click dismissed the dialog");
  await page.mouse.click(8, 8);
  await dialog.waitFor({ state: "detached" });
  await page.getByRole("button", { name: "创作目标", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "创作目标", exact: true });
  await choose(dialog, "作品形态", "网文连载");
  await dialog.getByRole("spinbutton", { name: /^全书目标字数/ }).fill("1000000");
  await dialog.getByRole("spinbutton", { name: /^目标章节数/ }).fill("500");
  check((await dialog.getByRole("status").textContent()).replaceAll(",", "").includes("1500000"), "Contradictory length targets were not explained");
  await dialog.getByRole("spinbutton", { name: /^默认章节目标字数/ }).fill("2000");
  const bodyScroll = await dialog.locator(".modal-body").evaluate(e => ({ height: e.clientHeight, content: e.scrollHeight, overflow: getComputedStyle(e).overflowY }));
  check(bodyScroll.height > 0 && bodyScroll.overflow === "auto", "Dialog does not own its scrollable body");
  await dialog.getByRole("button", { name: "估算章节数", exact: true }).click();
  await dialog.getByRole("button", { name: "保存创作目标", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  await page.getByText("网文连载", { exact: true }).waitFor();
  await page.screenshot({ path: `${artifacts}/discussion.png` });

  await navigate("大纲");
  await page.getByRole("button", { name: "新增规划", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "新增规划", exact: true });
  await dialog.getByRole("textbox", { name: "标题", exact: true }).fill("第一卷：北塔");
  await dialog.getByRole("spinbutton", { name: "结束章节", exact: true }).fill("10");
  await dialog.getByRole("spinbutton", { name: "目标字数", exact: true }).fill("20000");
  await dialog.getByRole("textbox", { name: "目标", exact: true }).fill("查明记忆交易的规则");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  await page.locator(".plan-card").filter({ has: page.getByRole("heading", { name: "第一卷：北塔", exact: true }) }).getByRole("button", { name: "新增规划", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "新增规划", exact: true });
  await choose(dialog, "层级", "章节");
  await dialog.getByRole("textbox", { name: "标题", exact: true }).fill("第一章：铜钥匙");
  await dialog.getByRole("spinbutton", { name: "结束章节", exact: true }).fill("1");
  await dialog.getByRole("spinbutton", { name: "目标字数", exact: true }).fill("2000");
  await dialog.getByRole("textbox", { name: "目标", exact: true }).fill("交出铜钥匙并付出代价");
  await dialog.getByRole("textbox", { name: "冲突", exact: true }).fill("交出钥匙将失去一段记忆");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  await page.getByRole("button", { name: "建立章节草稿", exact: true }).click();
  await navigate("正文");
  await page.getByRole("textbox", { name: "章节正文", exact: true }).fill("林舟把铜钥匙交给岑月。北塔的钟声随即响起，他再也想不起母亲的声音。");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  const memoryJob = await send("整理本章有原文依据的事实与人物变化，先作为建议让我审阅。");
  await confirmJob(memoryJob);
  await navigate("设定");
  await page.getByRole("tab", { name: /状态与伏笔/ }).click();
  await page.locator(".workspace-content").getByRole("heading", { name: "铜钥匙转移", exact: true }).waitFor();
  project = await command("get_project", { project_id: projectId });
  check(project.studio.memories.some(memory => memory.source_id === project.chapters[0].id && memory.quote === "林舟把铜钥匙交给岑月。"), "Memory adoption lost its source quote");
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("1");
  await page.locator(".workspace-content").getByRole("heading", { name: "铜钥匙转移", exact: true }).waitFor();
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("");

  await page.getByRole("button", { name: "新增记忆", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "新增记忆", exact: true });
  await choose(dialog, "记忆类型", "世界规则");
  await dialog.getByRole("textbox", { name: "标题", exact: true }).fill("交易必须付出记忆");
  await dialog.getByRole("textbox", { name: "内容", exact: true }).fill("每次交易都需要牺牲一段自己珍视的记忆，不能凭空免除代价。");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });

  await page.getByRole("button", { name: "新增记忆", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "新增记忆", exact: true });
  await choose(dialog, "记忆类型", "人物知情范围");
  await dialog.getByRole("textbox", { name: "标题", exact: true }).fill("钥匙位置的知情范围");
  await dialog.getByRole("textbox", { name: "内容", exact: true }).fill("铜钥匙的位置已经被知情者掌握。");
  await dialog.getByRole("textbox", { name: "知情人物", exact: true }).fill("林舟");
  await dialog.getByRole("spinbutton", { name: "章节", exact: true }).fill("1");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  await page.locator(".memory-card").filter({ has: page.getByRole("heading", { name: "钥匙位置的知情范围", exact: true }) }).getByRole("button", { name: "编辑", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "编辑记忆", exact: true });
  await dialog.getByRole("textbox", { name: "知情人物", exact: true }).fill("林舟\n岑月");
  await dialog.getByRole("spinbutton", { name: "章节", exact: true }).fill("12");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("6");
  let knowledgeCard = page.locator(".memory-card").filter({ has: page.getByRole("heading", { name: "钥匙位置的知情范围", exact: true }) });
  check(await knowledgeCard.count() === 1 && !(await knowledgeCard.innerText()).includes("岑月"), "A metadata edit erased or leaked later knowledge into chapter 6");
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("12");
  check(await knowledgeCard.count() === 1 && (await knowledgeCard.innerText()).includes("岑月"), "The later knowledge version is missing or duplicated");
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("");
  await mapAndSecondChapter(projectId);
  await page.getByRole("button", { name: "资料与搜索", exact: true }).click();
  await page.getByRole("tab", { name: "联网资料研究", exact: true }).click();
  await page.getByRole("button", { name: "搜索设置", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "搜索设置", exact: true });
  await dialog.getByRole("textbox", { name: "筛选搜索配置", exact: true }).fill("SearXNG");
  const row = dialog.locator("tbody tr").filter({ hasText: "SearXNG" });
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  const searchEditor = page.getByRole("dialog", { name: "搜索配置", exact: true });
  await searchEditor.getByRole("textbox", { name: "服务地址", exact: true }).fill(`${mock}/search`);
  await searchEditor.getByRole("switch", { name: "启用此搜索配置", exact: true }).check();
  await searchEditor.getByRole("button", { name: "保存搜索配置", exact: true }).click();
  await searchEditor.waitFor({ state: "detached" });
  await dialog.getByRole("switch", { name: "启用联网搜索", exact: true }).check();
  await choose(dialog, "默认搜索配置", "SearXNG");
  await dialog.getByRole("button", { name: "保存搜索策略", exact: true }).click();
  await dialog.getByRole("status").filter({ hasText: "已保存" }).last().waitFor();
  await dialog.getByRole("button", { name: "关闭对话框", exact: true }).click();
  await page.getByRole("textbox", { name: "检索词", exact: true }).fill("明朝锦衣卫官制");
  await page.getByRole("button", { name: "搜索资料", exact: true }).click();
  await page.getByRole("link", { name: "明代官制资料（验收夹具）", exact: true }).waitFor();
  check(await page.getByRole("link", { name: "明代官制资料（验收夹具）", exact: true }).getAttribute("href") === "https://example.org/history", "Research result lost its source URL");
  await page.getByRole("textbox", { name: "检索词", exact: true }).fill("唐朝官制：尚未发送的新查询");
  await page.getByRole("button", { name: "采纳到资料库", exact: true }).click();
  await page.getByRole("button", { name: "已保存", exact: true }).waitFor();
  project = await command("get_project", { project_id: projectId });
  check(project.sources.some(source => source.content.includes("明朝锦衣卫官制")) && !project.sources.some(source => source.content.includes("尚未发送的新查询")), "Adopting old results recorded an unsent query as provenance");
  await page.screenshot({ path: `${artifacts}/research.png` });

  await page.getByRole("tab", { name: "本地资料", exact: true }).click();
  await page.getByRole("button", { name: "添加资料", exact: true }).click();
  const referenceImport = page.getByRole("dialog", { name: "添加资料", exact: true });
  await referenceImport.locator('input[type="file"]').setInputFiles({ name: "参考小说.txt", mimeType: "text/plain", buffer: Buffer.from("t+LD5g0KtdrSu9XCIMrYxLkNCs/I0N66w7Swu6ehow0KtdrSu9XCINbYuLTVwrrFDQrU2brLttTEubLhoaMNCrXatv7VwiDAtL/NDQrAtL/Nwe3T0Mv5x/Ohow==", "base64") });
  await referenceImport.getByText(/识别编码.*gb18030/).waitFor();
  await referenceImport.getByRole("spinbutton", { name: "结束章节位置", exact: true }).fill("3");
  const importedExcerpt = await referenceImport.getByRole("textbox", { name: "资料内容", exact: true }).inputValue();
  check(importedExcerpt.includes("Original lines 2–7") && importedExcerpt.includes("第一章 重复章号") && importedExcerpt.includes("来客另有所求"), "Reference decoding or range selection lost original positions or duplicate headings");
  await referenceImport.getByRole("button", { name: "保存资料", exact: true }).click();
  await referenceImport.waitFor({ state: "detached" });
  project = await command("get_project", { project_id: projectId });
  check(project.sources.some(source => source.category === "craft" && source.content.replaceAll("\r\n", "\n") === importedExcerpt), "The selected reference range was not saved as a craft source");

  await page.getByRole("dialog", { name: "资料与搜索", exact: true }).getByRole("button", { name: "关闭对话框", exact: true }).click();
  await input.fill("失败期间输入的新草稿必须保留。");
  await page.locator(".sidebar-bottom").getByRole("button", { name: "设置", exact: true }).click();
  check(await page.getByText("高级与实验", { exact: true }).count() === 0, "Settings still exposes the retired experiment section");

  const archive = await command("export", { project_id: projectId, format: "archive" });
  const backup = JSON.parse(archive.content);
  check(backup.project.conversations.length >= 2 && backup.project.jobs.length >= 3, "Complete backup omitted conversation, proposal or task records");
  check(!archive.content.includes("provider_context"), "Backup exposed model-private continuation state");

  await navigate("作品");
  await page.waitForFunction(() => document.querySelector('textarea[aria-label="写作请求"]')?.value === "失败期间输入的新草稿必须保留。");
  check((await input.inputValue()) === "失败期间输入的新草稿必须保留。", "Returning from settings lost the saved conversation draft");
  await viewport(1280, 720);
  const height = await page.locator(".assistant-messages").evaluate(element => element.getBoundingClientRect().height);
  check(height >= 180, `Conversation was squeezed to ${height}px on a laptop viewport`);
  await page.screenshot({ path: `${artifacts}/laptop.png` });
  await viewport(1024, 768);
  check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "Studio overflows the viewport horizontally");
  await page.waitForFunction(() => document.querySelector(".workspace-assistant")?.matches(":modal"), undefined, { timeout: 15000 });
  await input.waitFor({ state: "visible" });
  check(await input.evaluate(element => element.closest("dialog")?.matches(":modal")), "Compact partner did not become a modal drawer");
  await page.screenshot({ path: `${artifacts}/compact.png` });
  await viewport(860, 640);
  const sendButton = await page.locator(".assistant-composer").getByRole("button", { name: "发送", exact: true }).boundingBox();
  check(sendButton && sendButton.y + sendButton.height <= 640, `Minimum desktop window hides the send button: ${JSON.stringify(sendButton)}`);
  await page.screenshot({ path: `${artifacts}/minimum.png` });
  await viewport(1440, 900);
  const repeatedText = "请继续讨论记忆交易的动机。";
  const repeatedFirst = await send(repeatedText);
  const repeatedSecond = await send(repeatedText);
  check(repeatedFirst.id !== repeatedSecond.id, "Two acknowledged identical submissions reused one job");

  let acceptedWithoutAck = "";
  const lostText = "这条请求用于验证提交确认丢失后的幂等重试。";
  const beforeLost = (await command("get_runs", { project_id: projectId })).length;
  await page.route("**/api/command", async route => {
    const request = route.request().postDataJSON();
    if (!acceptedWithoutAck && request?.command === "start_ai" && request.args?.instruction === lostText) {
      const response = await route.fetch();
      const payload = await response.json();
      check(payload.ok, payload.error || "Upstream submission failed");
      acceptedWithoutAck = payload.result.id;
      await route.abort("failed");
    } else await route.continue();
  });
  await input.fill(lostText);
  await input.press("Enter");
  await page.locator(".assistant-composer").getByRole("alert").waitFor();
  const recoveredAck = await send(lostText);
  await page.unroute("**/api/command");
  check(recoveredAck.id === acceptedWithoutAck && (await command("get_runs", { project_id: projectId })).length === beforeLost + 1, "An unacknowledged retry generated twice");

  await reviewAndSelectionRegressions(projectId);
  await chapterLifecycleRegressions({ page, command, check, navigate, originalProjectId: projectId, artifacts });

  await page.evaluate(() => Object.defineProperty(window, "showSaveFilePicker", { value: undefined, configurable: true }));
  const downloadEvent = page.waitForEvent("download");
  const exportFinished = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "finish_archive_transfer");
  await page.locator(".export-menu summary").click();
  await page.getByRole("button", { name: "完整作品备份", exact: true }).click();
  const downloaded = await downloadEvent;
  const backupPath = `${artifacts}/browser-backup.quillweave.json`;
  await downloaded.saveAs(backupPath);
  await exportFinished;
  await page.getByRole("button", { name: "我的作品", exact: true }).click();
  await page.getByRole("button", { name: "导入", exact: true }).first().click();
  const restoreDialog = page.getByRole("dialog", { name: "导入作品", exact: true });
  const lostBackupAcks = new Set();
  await page.route("**/api/command", async route => {
    const request = route.request().postDataJSON();
    if (["write_archive_chunk", "complete_archive_import"].includes(request?.command) && !lostBackupAcks.has(request.command)) {
      const response = await route.fetch();
      const payload = await response.json();
      check(payload.ok, payload.error || "Backup operation failed before acknowledgement");
      lostBackupAcks.add(request.command);
      await route.abort("failed");
    } else await route.continue();
  });
  await restoreDialog.locator('input[type="file"]').setInputFiles(backupPath);
  await restoreDialog.waitFor({ state: "detached" });
  await page.unroute("**/api/command");
  check(lostBackupAcks.size === 2, "Backup acceptance did not exercise both lost acknowledgements");
  const restoredStories = await command("list_projects");
  check(restoredStories.length === 2 && restoredStories.some(story => story.id !== projectId), "Browser backup import did not create a separate restored work");
  await legacyGoalSave();
  await page.locator(".sidebar-bottom").getByRole("button", { name: "设置", exact: true }).click();
  await page.locator(".model-editor").waitFor();
  check(await page.getByRole("heading", { name: "外部记忆设置", exact: true }).count() === 0, "Retired Hindsight settings remain visible");
  await regularFields(page.locator(".model-editor"), "model settings");
  const compact = await page.locator(".model-editor-heading .button").evaluateAll(elements => elements.map(e => ({height:e.getBoundingClientRect().height,font:getComputedStyle(e).fontSize})));
  check(compact.length > 0 && compact.every(e => e.height === 34 && e.font === "12px"), `Compact actions disagree: ${JSON.stringify(compact)}`);
  const profileToggle = page.getByRole("switch", { name: "启用此配置", exact: true });
  const beforeToggle = await profileToggle.isChecked();
  await profileToggle.focus();
  await profileToggle.press("Space");
  check(await profileToggle.isChecked() !== beforeToggle, "Switch ignored its keyboard action");
  await profileToggle.press("Space");
  check(await profileToggle.isChecked() === beforeToggle, "Switch did not restore its prior state");
  const toggleRow = await profileToggle.locator("xpath=ancestor::label").boundingBox();
  const protocolBox = await page.getByRole("combobox", { name: "API 协议", exact: true }).boundingBox();
  check(Math.abs(toggleRow.y + toggleRow.height - protocolBox.y - protocolBox.height) < 1, "Toggle is not aligned to the neighboring field");
  await secretInputs();
  check(await page.locator(".model-search-binding").count() === 1, "Switching model profiles accumulated duplicate search bindings");
  controlAudit.push({ section:"compact actions", fields:compact, switchKeyboard:true, toggleAlignment:true });
  await page.screenshot({ path: `${artifacts}/model-settings.png`, fullPage: true });
  check(failures.length === 0, failures.join("\n"));
  return { result: "PASS: studio workflow and unified controls", controlAudit };
}
