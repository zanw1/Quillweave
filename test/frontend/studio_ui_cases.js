/** Exercise author-visible workflows through Playwright CLI against the real isolated application. */
async page => {
  const failures = [];
  page.on("pageerror", error => failures.push(String(error)));
  const mock = "__STUDIO_MOCK_URL__";
  const artifacts = __STUDIO_ARTIFACT_DIR_JSON__;
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
    await page.getByRole("button", { name: "模型设置", exact: true }).click();
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

    await field("Hindsight 密钥", false);
    check(await page.getByRole("checkbox", { name: /清除密钥/ }).count() === 0, "Legacy clear checkboxes remain");
    const hindsight = page.getByLabel("Hindsight 密钥", { exact: true });
    await hindsight.fill("acceptance-only-memory-first");
    saved = await save("save_integrations", "保存记忆服务");
    check(saved.args.key_updates.hindsight.value === "acceptance-only-memory-first" && saved.result.key_status.hindsight, "Memory key did not save directly");
    check(!JSON.stringify(saved.result).includes("acceptance-only-"), "Memory response returned a key");
    await field("Hindsight 密钥", true);
    checks.push("memory field: blank, direct entry, masked after save");
    await page.getByRole("textbox", { name: "Hindsight 接口地址", exact: true }).fill(`${mock}/memory-review`);
    saved = await save("save_integrations", "保存记忆服务");
    check(Object.keys(saved.args.key_updates).length === 0 && !saved.result.key_status.hindsight, "Changing the credential destination retained a memory key");
    checks.push("memory endpoint change: old destination credential invalidated");
    await hindsight.fill("acceptance-only-memory-replacement");
    saved = await save("save_integrations", "保存记忆服务");
    check(saved.args.key_updates.hindsight.value === "acceptance-only-memory-replacement", "Memory replacement failed");
    await field("Hindsight 密钥", true);
    await page.getByRole("button", { name: "清除密钥 · Hindsight 密钥", exact: true }).click();
    saved = await save("save_integrations", "保存记忆服务");
    check(saved.args.key_updates.hindsight.action === "clear" && !saved.result.key_status.hindsight, "Memory clear did not persist");
    await field("Hindsight 密钥", false);
    checks.push("memory replace and clear: exact requests, blank afterward");

    await viewport(860, 640);
    await model.scrollIntoViewIfNeeded();
    check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "Direct credential controls overflow the minimum window");
    const geometry = await page.locator(".secret-input-row > input").evaluateAll(inputs => inputs.map(input => ({ height:input.getBoundingClientRect().height, font:getComputedStyle(input).fontSize, radius:getComputedStyle(input).borderRadius })));
    check(geometry.length === 2 && geometry.every(input => input.height === 40 && input.font === "13px" && input.radius === "8px"), "Secret inputs diverged from the shared field geometry");
    checks.push("minimum window and geometry: no overflow, 40px / 13px / 8px");
    await viewport(1440, 900);
    controlAudit.push({ section: "direct secret inputs", checks, geometry });
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  await choose(page, "Interface language", "中文");
  await page.getByRole("button", { name: "开始新故事", exact: true }).click();
  await page.getByRole("textbox", { name: "暂定名称（可选）", exact: true }).fill("北塔借忆人 · 完整验收");
  await page.getByRole("button", { name: "开始找灵感", exact: true }).click();
  await page.getByRole("navigation", { name: "创作工作台", exact: true }).waitFor();
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
  check(await page.getByRole("dialog").count() === 0, "Discussion opened an unsolicited dialog");
  check(await page.getByRole("button", { name: "根据讨论生成大纲", exact: true }).count() === 0, "Discussion prematurely pushed a complete outline");
  await page.getByRole("button", { name: "采纳", exact: true }).click();
  await page.locator(".proposal-card").getByText("已采纳", { exact: true }).waitFor();
  project = await command("get_project", { project_id: projectId });
  check(project.studio.decisions.length === 1, "Accepted proposal did not become shared author knowledge");

  await page.getByRole("button", { name: "写作方法", exact: true }).click();
  await page.locator(".method-card").first().getByRole("button").click();
  await page.locator(".drawer").getByRole("button", { name: "讨论这些问题", exact: true }).click();
  await page.waitForFunction(() => document.querySelector('textarea[aria-label="写作请求"]')?.value.includes("一句话"));
  await page.locator(`[data-job-id="${first.id}"] .answer-heading`).getByText("已完成", { exact: true }).waitFor();
  check((await input.inputValue()).includes("一句话"), "Applying a method to a story without chapters lost its seed while restoring the existing conversation");
  check((await command("get_project", { project_id: projectId })).chapters.length === 0, "A writing method unexpectedly required or created a chapter");

  await page.getByRole("button", { name: "重命名会话", exact: true }).click();
  await page.getByRole("textbox", { name: "会话名称", exact: true }).fill("北塔核心创意");
  await page.getByRole("dialog", { name: "重命名会话", exact: true }).getByRole("button", { name: "保存", exact: true }).click();
  await page.getByRole("button", { name: "北塔核心创意", exact: true }).waitFor();
  await page.getByRole("button", { name: "回退到这里", exact: true }).click();
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
  await page.getByRole("alert").last().waitFor();
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
  check(await dialog.getByRole("combobox", { name: "创作阶段", exact: true }).evaluate(e => document.activeElement === e), "Select Tab did not continue to the next form field");
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

  await page.getByRole("button", { name: "分层规划", exact: true }).click();
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
  await page.getByRole("button", { name: "章节写作", exact: true }).click();
  await page.getByRole("textbox", { name: "章节正文", exact: true }).fill("林舟把铜钥匙交给岑月。北塔的钟声随即响起，他再也想不起母亲的声音。");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await choose(page, "AI 任务", "整理章节记忆");
  await send("整理本章有原文依据的事实与人物变化，先作为建议让我审阅。");
  await page.getByRole("button", { name: "采纳", exact: true }).click();
  await page.getByRole("button", { name: "故事规划", exact: true }).click();
  await page.getByRole("button", { name: "故事记忆", exact: true }).click();
  await page.getByRole("heading", { name: "铜钥匙转移", exact: true }).waitFor();
  project = await command("get_project", { project_id: projectId });
  check(project.studio.memories.some(memory => memory.source_id === project.chapters[0].id && memory.quote === "林舟把铜钥匙交给岑月。"), "Memory adoption lost its source quote");
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("1");
  await page.getByRole("heading", { name: "铜钥匙转移", exact: true }).waitFor();
  await page.getByRole("spinbutton", { name: "查看指定章节的记忆", exact: true }).fill("");

  await page.getByRole("button", { name: "新增记忆", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "新增记忆", exact: true });
  await choose(dialog, "记忆类型", "世界规则");
  await dialog.getByRole("textbox", { name: "标题", exact: true }).fill("交易必须付出记忆");
  await dialog.getByRole("textbox", { name: "内容", exact: true }).fill("每次交易都需要牺牲一段自己珍视的记忆，不能凭空免除代价。");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });

  await page.getByRole("button", { name: "资料研究", exact: true }).click();
  await page.getByRole("button", { name: "故事记忆", exact: true }).click();
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
  await page.getByRole("button", { name: "资料研究", exact: true }).click();
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
  await dialog.getByRole("textbox", { name: "Hindsight 接口地址", exact: true }).fill(mock);
  await dialog.getByRole("button", { name: "保存记忆服务", exact: true }).click();
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

  await page.getByRole("button", { name: "方法与实验", exact: true }).click();
  await page.locator(".external-memory summary").filter({ hasText: "选择要发送的记忆" }).click();
  await page.getByRole("checkbox", { name: /交易必须付出记忆/ }).check();
  await page.getByRole("button", { name: "存入所选记忆", exact: true }).click();
  await page.locator(".external-result").filter({ hasText: "测试记忆响应" }).waitFor();
  await page.getByRole("textbox", { name: "检索词", exact: true }).fill("记忆交易代价");
  await page.getByRole("button", { name: "召回", exact: true }).click();
  await page.getByRole("button", { name: "反思分析", exact: true }).click();
  await page.getByRole("button", { name: "新增评测案例", exact: true }).click();
  await page.getByRole("combobox", { name: "已完成候选稿", exact: true }).click();
  const popupFrames = await page.locator(".qw-select-popup").evaluate(async element => {
    const samples = [];
    for (let i = 0; i < 4; i++) { await new Promise(requestAnimationFrame); const b = element.getBoundingClientRect(); samples.push({ x: b.x, y: b.y, bottom: b.bottom }); }
    return samples;
  });
  check(popupFrames.every(b => b.y >= 0 && b.bottom <= 900 && Math.abs(b.y - popupFrames[0].y) < .5), `Popup moved or overflowed: ${JSON.stringify(popupFrames)}`);
  await page.getByRole("option").filter({ hasNotText: "已完成候选稿" }).first().click();
  await page.getByRole("button", { name: "添加候选稿", exact: true }).click();
  await page.getByRole("spinbutton", { name: "质量", exact: false }).fill("4");
  await page.getByRole("checkbox", { name: "作者已确认评分", exact: true }).check();
  await page.getByRole("button", { name: "评估回放", exact: true }).click();
  await page.getByText("尚无充分晋升证据", { exact: true }).waitFor();
  await page.getByRole("button", { name: "保存实验", exact: true }).click();
  await page.locator(".evaluation-lab").getByRole("status").filter({ hasText: "已保存" }).waitFor();

  const archive = await command("export", { project_id: projectId, format: "archive" });
  const backup = JSON.parse(archive.content);
  check(backup.project.conversations.length >= 3 && backup.project.jobs.length >= 3 && backup.project.experiments.length === 1, "Complete backup omitted conversation, proposal or experiment records");
  check(!archive.content.includes("provider_context"), "Backup exposed model-private continuation state");

  await page.getByRole("button", { name: "共创讨论", exact: true }).click();
  await page.waitForFunction(() => document.querySelector('textarea[aria-label="写作请求"]')?.value === "失败期间输入的新草稿必须保留。");
  check((await input.inputValue()) === "失败期间输入的新草稿必须保留。", "Returning from another tab resurrected an already-consumed writing method over the saved draft");
  await viewport(1280, 720);
  const height = await page.locator(".assistant-messages").evaluate(element => element.getBoundingClientRect().height);
  check(height >= 180, `Conversation was squeezed to ${height}px on a laptop viewport`);
  await page.screenshot({ path: `${artifacts}/laptop.png` });
  await viewport(1024, 768);
  check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "Studio overflows the viewport horizontally");
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
  await page.getByRole("alert").last().waitFor();
  const recoveredAck = await send(lostText);
  await page.unroute("**/api/command");
  check(recoveredAck.id === acceptedWithoutAck && (await command("get_runs", { project_id: projectId })).length === beforeLost + 1, "An unacknowledged retry generated twice");

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
  await page.getByRole("button", { name: "模型设置", exact: true }).click();
  await page.getByRole("heading", { name: "外部记忆设置", exact: true }).waitFor();
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
