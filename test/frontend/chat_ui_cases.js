/** Exercise the real chat transport and rendered controls using disposable models and story data. */
async page => {
  const checks = [];
  const failures = [];
  const artifacts = __STUDIO_ARTIFACT_DIR_JSON__;
  page.on("pageerror", error => failures.push(String(error)));

  /** Fail the acceptance at the first unmet invariant and retain a named successful check. */
  function check(condition, name) {
    if (!condition) throw new Error(name);
    checks.push(name);
  }

  /** Use the application's production command envelope against this test's isolated backend. */
  async function command(name, args = {}) {
    return page.evaluate(async ({ name, args }) => {
      const response = await fetch("/api/command", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Quillweave-Client": "1" },
        body: JSON.stringify({ command: name, args: { ...args, ui_language: "zh-CN" } }),
      });
      const payload = await response.json();
      if (!payload.ok) throw new Error(payload.error);
      return payload.result;
    }, { name, args });
  }

  /** Follow a submission from the real composer to its accepted durable job. */
  async function send(text) {
    const accepted = page.waitForResponse(response =>
      response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai",
    );
    const input = page.getByRole("textbox", { name: "写作请求", exact: true });
    await input.fill(text);
    await input.press("Enter");
    const payload = await (await accepted).json();
    if (!payload.ok) throw new Error(payload.error);
    return payload.result;
  }

  /** Wait for a persisted terminal job rather than assuming the visible animation has finished. */
  async function terminal(id) {
    await page.locator(`[data-job-id="${id}"] .answer-heading .badge`)
      .filter({ hasText: /^(已完成|已取消|未完成)$/ }).waitFor();
    return command("get_job", { job_id: id });
  }

  /** Keep textarea and toolbar inside one card at automatic, minimum and expanded composer heights. */
  async function composerLayout() {
    const input = page.getByRole("textbox", { name: "写作请求", exact: true });

    /** Select a real saved connection through the model control before measuring its inline reasoning label. */
    async function chooseModel(name) {
      await input.fill("/model");
      await input.press("Enter");
      const dialog = page.getByRole("dialog", { name: "本会话模型", exact: true });
      await dialog.getByRole("combobox", { name: "会话模型", exact: true }).click();
      await page.getByRole("option", { name: new RegExp(name) }).click();
      await dialog.getByRole("button", { name: "完成", exact: true }).click();
    }

    /** Check rendered rectangles after layout settles, including overflow caused by native textarea minima. */
    async function measure(name) {
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const dimensions = await page.locator(".chat-input-shell").evaluate(shell => {
        const box = element => element.getBoundingClientRect().toJSON();
        const footer = shell.querySelector(".chat-composer-footer");
        const send = footer.querySelector(".chat-send-button");
        return {
          shell: box(shell),
          text: box(shell.querySelector("textarea")),
          footer: box(footer),
          send: box(send),
          buttons: [...footer.querySelectorAll("button")].map(box),
          radius: getComputedStyle(send).borderRadius,
          background: getComputedStyle(send).backgroundColor,
        };
      });
      check(dimensions.text.bottom <= dimensions.footer.top + .5 &&
        dimensions.buttons.every(button => button.top >= dimensions.footer.top && button.bottom <= dimensions.shell.bottom && button.left >= dimensions.shell.left && button.right <= dimensions.shell.right),
      `Composer ${name} keeps text and all toolbar buttons in separate non-overlapping card regions`);
      check(Math.abs(dimensions.send.width - dimensions.send.height) < 1 && dimensions.radius === "50%" && dimensions.background === "rgb(21, 21, 21)",
        `Composer ${name} uses the reference circular dark send button`);
    }

    await chooseModel("布局参考模型");
    check(await page.locator(".chat-model-reasoning").innerText() === "很高", "The model control combines the real model name and reasoning effort");
    const handle = page.getByRole("separator", { name: "拖动调整输入区高度", exact: true });
    for (const [width, height] of [[1440, 1000], [1280, 720], [1024, 768], [860, 640], [390, 844]]) {
      await page.setViewportSize({ width, height });
      if (width < 1280) {
        await page.waitForFunction(() => document.querySelector(".workspace-assistant")?.matches(":modal"), undefined, { timeout: 15000 });
      }
      await handle.dblclick();
      await input.fill("布局验收，多行正文在输入区内部滚动。\n".repeat(20));
      await measure(`${width}x${height} automatic`);
      await handle.focus();
      await handle.press("Shift+ArrowDown");
      await measure(`${width}x${height} minimum`);
      await handle.press("Shift+ArrowUp");
      await handle.press("Shift+ArrowUp");
      await input.evaluate(element => { element.scrollTop = element.scrollHeight; });
      await measure(`${width}x${height} expanded and scrolled`);
      if ([1440, 390].includes(width)) {
        await page.screenshot({ path: `${artifacts}/composer-reference-${width}.png` });
      }
    }
    await input.fill("实际拖拽输入区高度");
    const oldHeight = Number(await handle.getAttribute("aria-valuenow"));
    const handleBox = await handle.boundingBox();
    await page.mouse.move(handleBox.x + handleBox.width / 2, handleBox.y + handleBox.height / 2);
    await page.mouse.down();
    await page.mouse.move(handleBox.x + handleBox.width / 2, handleBox.y - 70, { steps: 6 });
    await page.mouse.up();
    check(Number(await handle.getAttribute("aria-valuenow")) > oldHeight, "Dragging the actual handle increases composer height");
    await measure("pointer-resized");
    await input.fill("/");
    await page.getByRole("option").filter({ hasText: "/model" }).waitFor();
    await page.setViewportSize({ width: 420, height: 844 });
    const menu = await page.getByRole("listbox", { name: "聊天命令", exact: true }).boundingBox();
    check(menu && menu.y >= 0 && menu.y + menu.height <= 844, "The command palette follows the resized text area within its viewport");
    await input.press("Escape");
    await handle.dblclick();
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.waitForFunction(() => !document.querySelector(".workspace-assistant")?.matches(":modal"), undefined, { timeout: 15000 });
    await chooseModel("第二个聊天模型");
  }

  /** Measure real dialog gaps and scroll ownership across five supported window sizes. */
  async function skillDialogLayout() {
    for (const [width, height] of [[1440, 1000], [1280, 720], [1024, 768], [860, 640], [390, 844]]) {
      await page.setViewportSize({ width, height });
      if (width < 1280) {
        await page.waitForFunction(() => document.querySelector(".workspace-assistant")?.matches(":modal"), undefined, { timeout: 15000 });
      }
      const composer = page.getByRole("textbox", { name: "写作请求", exact: true });
      await composer.fill("/skill");
      await composer.press("Enter");
      const dialog = page.getByRole("dialog", { name: "技能", exact: true });
      await dialog.locator(".chat-skill-choice").first().waitFor();
      const footer = dialog.locator(":scope > footer.modal-actions");
      const buttons = footer.getByRole("button");
      const initial = await footer.boundingBox();
      const dialogBox = await dialog.boundingBox();
      const reload = await buttons.nth(0).boundingBox();
      const done = await buttons.nth(1).boundingBox();
      const horizontalInset = await footer.evaluate(element => parseFloat(getComputedStyle(element).paddingRight));
      check(initial && dialogBox && reload && done &&
        done.x - reload.x - reload.width >= 7 &&
        horizontalInset >= 20 && dialogBox.x + dialogBox.width - done.x - done.width >= horizontalInset - 1 &&
        done.y + done.height <= initial.y + initial.height - 15,
      `Skill dialog ${width}x${height} keeps its collapsed footer spaced and inside its border`);

      const choice = dialog.locator(".chat-skill-choice").first();
      const checkbox = await choice.getByRole("checkbox").boundingBox();
      const title = await choice.locator("strong").boundingBox();
      check(checkbox && title && title.x >= checkbox.x + checkbox.width + 8 && Math.abs(title.y - checkbox.y) <= 6,
        `Skill dialog ${width}x${height} aligns checkboxes beside their titles`);
      await dialog.locator(".chat-skill-directories > summary").click();
      const save = dialog.getByRole("button", { name: "保存并重新发现技能", exact: true });
      await save.scrollIntoViewIfNeeded();
      const fieldBox = await dialog.getByRole("textbox", { name: "技能目录", exact: true }).boundingBox();
      const saveBox = await save.boundingBox();
      const expandedFooter = await footer.boundingBox();
      check(fieldBox && saveBox && expandedFooter && saveBox.y - fieldBox.y - fieldBox.height >= 11 &&
        saveBox.y + saveBox.height <= expandedFooter.y && expandedFooter.y + expandedFooter.height <= height,
      `Skill dialog ${width}x${height} separates directory saving from the expanded footer`);
      await dialog.locator(".modal-body").evaluate(element => { element.scrollTop = 0; });
      const scrolledFooter = await footer.boundingBox();
      check(scrolledFooter && expandedFooter && Math.abs(scrolledFooter.y - expandedFooter.y) <= 1,
        `Skill dialog ${width}x${height} keeps footer actions outside the scrolling body`);
      await save.scrollIntoViewIfNeeded();
      if ([1440, 860, 390].includes(width)) {
        await page.screenshot({ path: `${artifacts}/skills-expanded-${width}.png` });
      }
      if (width === 1440) {
        const saved = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "set_skill_roots");
        await save.click();
        const payload = await (await saved).json();
        check(payload.ok, "The separated directory save action still persists and reloads skills");
        await page.setViewportSize({ width: 860, height: 640 });
        await footer.getByRole("button", { name: "重新加载", exact: true }).click();
        check(await footer.getByRole("button", { name: "完成", exact: true }).isVisible(),
          "Resizing with an open skill dialog keeps its actions above the underlying chat drawer");
      }
      await footer.getByRole("button", { name: "完成", exact: true }).click();
      await dialog.waitFor({ state: "detached" });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.waitForFunction(() => !document.querySelector(".workspace-assistant")?.matches(":modal"), undefined, { timeout: 15000 });
  }

  await page.setViewportSize({ width: 1440, height: 1000 });
  const project = await command("create_project", { title: "聊天工作区验收", genre: "奇幻", premise: "灯塔守护者寻找失去的记忆" });
  await page.evaluate(id => {
    localStorage.setItem("quillweave-language", "zh-CN");
    localStorage.setItem("quillweave-project", id);
  }, project.id);
  await page.reload();
  await page.locator(".project-name").filter({ hasText: project.title }).click();
  const input = page.getByRole("textbox", { name: "写作请求", exact: true });
  await input.waitFor({ state: "visible" });
  check(await page.evaluate(() => !document.activeElement?.matches('.panel-resize')), "Opening chat keeps initial focus away from resize handles");
  const widthHandle = page.getByRole("separator", { name: "拖动调整聊天宽度", exact: true });
  await page.keyboard.press("Tab");
  await widthHandle.focus();
  check(await widthHandle.evaluate(element => getComputedStyle(element).outlineStyle === "none" && getComputedStyle(element, "::after").outlineStyle === "solid"), "Keyboard focus is localized to the resize grip");
  await input.focus();
  await input.fill("focus checkpoint");
  await input.evaluate(element => element.setSelectionRange(2, 7));
  for (const [width, height, modal] of [[1024, 768, true], [1440, 1000, false]]) {
    await page.setViewportSize({ width, height });
    await page.waitForFunction(expected => document.querySelector('.workspace-assistant')?.matches(':modal') === expected, modal);
    check(await input.evaluate(element => document.activeElement === element && element.selectionStart === 2 && element.selectionEnd === 7), "Layout switching preserves composer focus and selection");
  }
  await input.fill("");

  await input.waitFor();

  await input.fill("/");
  await page.getByRole("option").filter({ hasText: "/model" }).waitFor();
  check(await page.locator(".quick-intent-bar").count() === 0, "Slash commands replace the permanent intent toolbar");
  await input.fill("/model");
  await input.press("Enter");
  const models = page.getByRole("dialog", { name: "本会话模型", exact: true });
  await models.waitFor();
  await models.getByRole("combobox", { name: "会话模型", exact: true }).click();
  await page.getByRole("option", { name: /第二个聊天模型/ }).click();
  await models.getByRole("button", { name: "完成", exact: true }).click();
  check(await page.getByRole("button", { name: "选择模型", exact: true }).innerText() === "chat-fixture-second", "The composer displays the chosen model");

  await skillDialogLayout();

  await input.fill("/skill chat-acceptance");
  await input.press("Enter");
  await page.locator(".assistant-composer").getByText("chat-acceptance", { exact: true }).waitFor();
  await page.evaluate(() => localStorage.setItem("quillweave-composer-height", "226"));
  await page.reload();
  await page.locator(".project-name").filter({ hasText: project.title }).click();
  await input.waitFor();
  await page.waitForFunction(() => {
    const composer = document.querySelector(".assistant-composer");
    const send = composer?.querySelector(".chat-send-button");
    return composer && send && composer.getBoundingClientRect().height <= 400 && composer.scrollHeight <= composer.clientHeight + 2 && send.getBoundingClientRect().bottom <= innerHeight;
  });
  check(true, "A restored short composer grows to keep skills, save policy and Send visible without scrolling");
  await composerLayout();
  const first = await send("CHAT_MODEL 带着所选技能讨论人物");
  const firstDone = await terminal(first.id);
  check(firstDone.status === "done" && firstDone.model_selection?.profile_id === "chat-second", `The accepted request freezes the selected model: ${JSON.stringify({status: firstDone.status, selection: firstDone.model_selection, error: firstDone.error})}`);
  check(firstDone.prompt.skills.some(skill => skill.name === "chat-acceptance"), "The actual selected skill is versioned in the request");
  check(firstDone.prompt.budget.can_send && firstDone.prompt.budget.estimated, "Context preview exposes the same estimated token budget used for sending");

  const slow = await send("CHAT_STREAM 逐段展示并允许停止");
  const thought = page.locator(`[data-job-id="${slow.id}"] .chat-reasoning`);
  await thought.getByText(/先核对人物当前处境/).waitFor();
  check((await command("get_job", { job_id: slow.id })).status === "running", "Model thought text is visible before the request completes");
  const slowAnswer = page.locator(`[data-job-id="${slow.id}"] .markdown-response`);
  await slowAnswer.getByText(/第一段正在输出/).waitFor();
  const partial = await command("get_job", { job_id: slow.id });
  check(partial.status === "running" && partial.text.includes("第一段"), "Text is visible while the real model request is still running");
  await page.getByRole("button", { name: "停止", exact: true }).click();
  const cancelled = await terminal(slow.id);
  check(cancelled.status === "cancelled" && cancelled.text.includes("第一段"), "Stopping retains partial prose in the durable job");
  check(cancelled.activity[0].kind === "reasoning" && cancelled.activity[1].kind === "text", "Thought and prose retain their actual stream order");
  await send("CHAT_MODEL 停止后继续一个新回合").then(job => terminal(job.id));
  await page.locator(`[data-job-id="${slow.id}"]`).getByText(/第一段正在输出/).waitFor();
  check(await page.locator(`[data-job-id="${slow.id}"]`).count() === 1, "A cancelled reply stays visible after the next turn");
  await page.reload();
  await page.locator(".project-name").filter({ hasText: project.title }).click();
  await input.waitFor();
  await page.locator(`[data-job-id="${slow.id}"]`).getByText(/第一段正在输出/).waitFor();
  check(await page.getByRole("button", { name: "选择模型", exact: true }).innerText() === "chat-fixture-second", "Reload restores the conversation model and cancelled history");
  await page.locator(`[data-job-id="${slow.id}"] .chat-reasoning > summary`).click();
  await page.locator(`[data-job-id="${slow.id}"] .chat-reasoning`).getByText(/先核对人物当前处境/).waitFor();
  check(true, "Reload restores expandable model thought content");

  const lostText = "CHAT_MODEL 幂等确认丢失验收";
  let lostJob = "";
  await page.route("**/api/command", async route => {
    const body = route.request().postDataJSON();
    if (!lostJob && body?.command === "start_ai" && body.args?.instruction === lostText) {
      const upstream = await route.fetch();
      const payload = await upstream.json();
      if (!payload.ok) throw new Error(payload.error);
      lostJob = payload.result.id;
      await route.abort("failed");
    } else await route.continue();
  });
  await input.fill(lostText);
  await input.press("Enter");
  await page.locator(".assistant-composer").getByRole("alert").waitFor();
  check(await input.inputValue() === lostText, "A lost acknowledgement preserves the submitted draft");
  const recovered = await send(lostText);
  await terminal(recovered.id);
  await page.unroute("**/api/command");
  check(recovered.id === lostJob, "A retry after a lost acknowledgement reuses its accepted job");

  const older = await command("create_conversation", { project_id: project.id, title: "UI_ARCHIVE_TARGET" });
  for (let index = 0; index < 101; index++) {
    await command("create_conversation", { project_id: project.id, title: `UI_RECENT_${index}` });
  }
  await input.fill("/resume");
  await input.press("Enter");
  const sessions = page.getByRole("dialog", { name: "会话", exact: true });
  await sessions.getByRole("textbox", { name: "搜索会话", exact: true }).fill("UI_ARCHIVE_TARGET");
  const olderRow = sessions.locator(".chat-session-row").filter({ hasText: "UI_ARCHIVE_TARGET" });
  await olderRow.waitFor();
  check(await olderRow.count() === 1, "Conversation search finds a record outside the first hundred sessions");
  await olderRow.getByRole("button", { name: "归档会话", exact: true }).click();
  await olderRow.waitFor({ state: "detached" });
  await sessions.getByRole("checkbox").check();
  await olderRow.waitFor();
  await olderRow.getByRole("button", { name: "恢复会话", exact: true }).click();
  await olderRow.waitFor({ state: "detached" });
  const restored = await command("get_conversation", { project_id: project.id, session_id: older.id });
  check(!restored.archived, "The conversation list archives and restores the same durable session");
  await sessions.getByRole("button", { name: "关闭对话框", exact: true }).click();

  await input.fill("/search");
  await input.press("Enter");
  const search = page.getByRole("dialog", { name: "联网搜索", exact: true });
  await search.waitFor();
  await search.press("Escape");
  await search.waitFor({ state: "detached" });
  check(await input.isVisible(), "Escape closes the inner search dialog without closing chat");

  await input.fill("/does-not-exist");
  await input.press("Enter");
  check(await input.inputValue() === "/does-not-exist", "Unknown commands preserve the author's input");
  await send("CHAT_MODEL 继续保留正常的聊天状态").then(job => terminal(job.id));
  await page.locator(".chat-token-usage").waitFor();
  await page.screenshot({ path: `${artifacts}/chat-wide.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(() => document.querySelector(".workspace-assistant.is-drawer")?.matches(":modal"), undefined, { timeout: 15000 });
  await input.waitFor();
  const inputBox = await input.boundingBox();
  check(inputBox && inputBox.x >= 0 && inputBox.x + inputBox.width <= 390, "The narrow composer is actually inside the visible modal viewport");
  check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "The narrow chat fits its supported viewport");
  await page.screenshot({ path: `${artifacts}/chat-narrow.png` });
  check(failures.length === 0, `No browser exceptions: ${failures.join("; ")}`);
  return { result: "PASS: chat workspace", checks };
}
