/** Exercise chapter drafts and recoverable deletion against the isolated real application. */
async ({ page, command, check, navigate, originalProjectId, artifacts }) => {
    const original = await command("get_project", { project_id: originalProjectId, artifacts });
    let project = await command("create_project", { title: "Chapter lifecycle regression", genre: "Mystery", premise: "Keep the latest manuscript" });
    let releaseSave;
    try {
      for (const title of ["Lifecycle A", "Lifecycle B"]) {
        project = await command("new_chapter", { project_id: project.id, revision: project.revision, title, target_words: null });
        project = await command("save_chapter", { project_id: project.id, revision: project.revision, chapter_id: project.chapters.at(-1).id, title, body: `SAVED_${title}`, synopsis: "", status: "draft", target_words: null });
      }
      const firstId = project.chapters[0].id;
      await page.evaluate(id => localStorage.setItem("quillweave-project", id), project.id);
      await page.reload();
      await page.locator(".project-name").filter({ hasText: project.title }).click();
      await navigate("正文");
      const body = page.getByRole("textbox", { name: "章节正文", exact: true });
      await page.evaluate(id => {
        const originalSet = Storage.prototype.setItem;
        Storage.prototype.setItem = function(key, value) {
          if (key.startsWith(`quillweave-draft-${id}-`)) throw new DOMException("Fixture quota", "QuotaExceededError");
          return originalSet.call(this, key, value);
        };
        window.__restoreDraftStorage = () => { Storage.prototype.setItem = originalSet; };
      }, project.id);
      await body.fill("CACHE_FAILURE_LATEST_TEXT");
      await page.locator(".chapter-item").filter({ hasText: "Lifecycle B" }).click();
      await page.locator(".chapter-item").filter({ hasText: "Lifecycle A" }).click();
      check(await body.inputValue() === "CACHE_FAILURE_LATEST_TEXT", "Storage failure lost input after switching chapters");
      await navigate("大纲");
      await navigate("正文");
      check(await body.inputValue() === "CACHE_FAILURE_LATEST_TEXT", "Storage failure lost input after leaving the editor");
      await page.getByRole("button", { name: "我的作品", exact: true }).click();
      await page.locator(".project-name").filter({ hasText: original.title }).click();
      await page.getByRole("button", { name: "我的作品", exact: true }).click();
      await page.locator(".project-name").filter({ hasText: project.title }).click();
      await navigate("正文");
      check(await body.inputValue() === "CACHE_FAILURE_LATEST_TEXT", "Storage failure lost input after switching projects");
      await page.evaluate(() => { window.__restoreDraftStorage(); delete window.__restoreDraftStorage; });
      const savedCache = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "save_chapter");
      await page.locator(".editor-toolbar").getByRole("button", { name: "保存", exact: true }).click();
      check((await (await savedCache).json()).ok, "Retained cache draft could not be saved");
      check((await command("get_project", { project_id: project.id })).chapters[0].body === "CACHE_FAILURE_LATEST_TEXT", "Retained draft did not reach the saved chapter");

      await body.fill("LOCAL_CONFLICT_TEXT");
      project = await command("get_project", { project_id: project.id });
      project = await command("save_chapter", { project_id: project.id, revision: project.revision, chapter_id: firstId, title: "Lifecycle A", body: "REMOTE_CONFLICT_TEXT", synopsis: "", status: "draft", target_words: null });
      await page.getByTestId("resolve-chapter-conflict").waitFor();
      await navigate("大纲");
      await navigate("正文");
      await page.getByTestId("resolve-chapter-conflict").click();
      const conflict = page.getByRole("dialog").filter({ has: page.getByTestId("keep-local-chapter") });
      check((await conflict.textContent()).includes("LOCAL_CONFLICT_TEXT") && (await conflict.textContent()).includes("REMOTE_CONFLICT_TEXT"), "Conflict resolution did not display both versions");
      check((await conflict.locator(".chapter-conflict-base").textContent()).includes("CACHE_FAILURE_LATEST_TEXT"), "Conflict comparison lost the actual common baseline after editor remount");
      await page.getByTestId("keep-local-chapter").click();
      const resolvedSave = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "save_chapter");
      await page.locator(".editor-toolbar").getByRole("button", { name: "保存", exact: true }).click();
      check((await (await resolvedSave).json()).ok, "Resolved draft still could not be submitted");
      project = await command("get_project", { project_id: project.id });
      check(project.chapters[0].body === "LOCAL_CONFLICT_TEXT" && project.chapters[0].versions.some(version => version.body === "REMOTE_CONFLICT_TEXT"), "Resolving a conflict lost the remote history");

      let saves = 0;
      let firstSaveStarted;
      const startedSave = new Promise(resolve => { firstSaveStarted = resolve; });
      const saveGate = new Promise(resolve => { releaseSave = resolve; });
      await page.route("**/api/command", async route => {
        const request = route.request().postDataJSON();
        if (request?.command === "save_chapter" && request.args.project_id === project.id) {
          saves++;
          if (saves === 1) {
            const response = await route.fetch();
            firstSaveStarted();
            await saveGate;
            await route.fulfill({ response });
            return;
          }
        }
        if (request?.command === "start_ai" && request.args.project_id === project.id) {
          await route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ ok: false, error: "Editor regression stops before model dispatch" }) });
          return;
        }
        await route.continue();
      });
      await body.fill("PENDING_SAVE_A");
      await page.locator(".editor-toolbar").getByRole("button", { name: "保存", exact: true }).click();
      await startedSave;
      await body.fill("PENDING_SAVE_B");
      const aiRequest = page.waitForRequest(request => request.url().endsWith("/api/command") && request.postDataJSON()?.command === "start_ai");
      const preparedRequest = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "prepare_ai");
      const composer = page.getByRole("textbox", { name: "写作请求", exact: true });
      await composer.fill("Use the current chapter draft");
      await composer.press("Enter");
      releaseSave();
      const started = await aiRequest;
      project = await command("get_project", { project_id: project.id });
      check(saves === 2 && project.chapters[0].body === "PENDING_SAVE_B", `AI started before the latest draft was persisted: ${saves}`);
      const prepared = await (await preparedRequest).json();
      const expectedHash = await page.evaluate(async text => [...new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text)))].map(byte => byte.toString(16).padStart(2, "0")).join(""), "PENDING_SAVE_B");
      check(prepared.ok && prepared.result.source_hash === expectedHash && started.postDataJSON().args.chapter_id === firstId, "AI prepared an outdated source snapshot");
      check(await body.inputValue() === "PENDING_SAVE_B", "Pending save replaced the current input");
      await page.unroute("**/api/command");

      await page.getByTestId("delete-chapter").click();
      const deletion = page.getByRole("dialog").filter({ has: page.getByTestId("confirm-delete-chapter") });
      await deletion.waitFor();
      await deletion.getByRole("button", { name: "取消", exact: true }).click();
      project = await command("get_project", { project_id: project.id });
      check(project.chapters.length === 2 && project.studio.changesets.at(-1).status === "rejected", "Cancelling deletion left a live pending deletion");
      const beforeDeletion = project.chapters.find(chapter => chapter.id === firstId);
      await page.getByTestId("delete-chapter").click();
      await page.getByTestId("confirm-delete-chapter").click();
      await deletion.waitFor({ state: "detached" });
      project = await command("get_project", { project_id: project.id });
      check(project.chapters.length === 1 && project.chapters[0].number === 2, "Deletion failed or silently renumbered the survivor");
      check(await page.getByRole("textbox", { name: "章节名称", exact: true }).inputValue() === "Lifecycle B", "Deletion did not select the surviving chapter");
      await page.getByTestId("undo-delete-chapter").click();
      await page.locator(".chapter-item").filter({ hasText: "Lifecycle A" }).waitFor();
      project = await command("get_project", { project_id: project.id });
      check(JSON.stringify(project.chapters.find(chapter => chapter.id === firstId)) === JSON.stringify(beforeDeletion), "Undo did not restore the entire chapter and version history");
      for (let remaining = 2; remaining > 0; remaining--) {
        await page.getByTestId("delete-chapter").click();
        await page.getByTestId("confirm-delete-chapter").click();
        await deletion.waitFor({ state: "detached" });
      }
      await page.getByRole("button", { name: "创建第一章", exact: true }).waitFor();
      await page.getByTestId("undo-delete-chapter").click();
      await body.waitFor();
      check((await command("get_project", { project_id: project.id })).chapters.length === 1, "The last chapter could not be recovered");

      project = await command("get_project", { project_id: project.id });
      const recoveryTarget = project.chapters[0];
      const originalBody = recoveryTarget.body;
      const external = await command("stage_changeset", {
        project_id: project.id,
        changeset: {
          id: `external-ui-${Date.now()}`, project_id: project.id, source: "external", commit_policy: "review",
          base_revision: project.revision, summary: "External integration acceptance",
          provenance: { client: "UI acceptance agent", transport: "mcp", review: { summary: "External review is recorded.", issues: [], needs_revision: false } },
          operations: [{ id: "external-prose", target_kind: "chapter", target_id: recoveryTarget.id, op_type: "replace_prose", title: "External chapter candidate", after_value: { body: "EXTERNAL_CANDIDATE_BODY" } }],
        },
      });
      await navigate("作品");
      const externalCard = page.locator(`[data-changeset-id="${external.id}"]`);
      await externalCard.waitFor();
      await externalCard.getByText("外部创作来源 · UI acceptance agent", { exact: true }).click();
      await externalCard.getByText("外部审阅 · External review is recorded.", { exact: true }).waitFor();
      await externalCard.getByRole("button", { name: "确认写入", exact: true }).click();
      await externalCard.getByRole("button", { name: "一键撤销", exact: true }).waitFor();
      project = await command("get_project", { project_id: project.id });
      check(project.chapters.find(chapter => chapter.id === recoveryTarget.id).body === "EXTERNAL_CANDIDATE_BODY", "The external candidate was not saved");
      project = await command("save_chapter", { project_id: project.id, revision: project.revision, chapter_id: recoveryTarget.id, title: recoveryTarget.title, body: "POST_APPLY_AUTHOR_EDIT", synopsis: recoveryTarget.synopsis, status: recoveryTarget.status, target_words: null });
      const recoveryResponse = page.waitForResponse(response => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "prepare_revert");
      await externalCard.getByRole("button", { name: "生成恢复候选", exact: true }).click();
      const recoveryResult = await (await recoveryResponse).json();
      check(recoveryResult.ok, "Preparing recovery failed");
      const recoveryCard = page.locator(`[data-changeset-id="${recoveryResult.result.changeset.id}"]`);
      await recoveryCard.waitFor();
      check(await recoveryCard.getByRole("button", { name: "确认写入", exact: true }).isDisabled(), "An unresolved recovery could be confirmed");
      check(await recoveryCard.getByText("POST_APPLY_AUTHOR_EDIT", { exact: true }).count() > 0, "The three-way comparison omitted the current author edit");
      if (artifacts) await page.screenshot({ path: `${artifacts}/external-recovery.png`, fullPage: true });
      await recoveryCard.getByRole("combobox").first().click();
      await page.getByRole("option", { name: "恢复原值", exact: true }).click();
      await recoveryCard.getByRole("button", { name: "更新预览", exact: true }).click();
      await recoveryCard.getByRole("button", { name: "确认写入", exact: true }).click();
      await recoveryCard.getByRole("button", { name: "一键撤销", exact: true }).waitFor();
      project = await command("get_project", { project_id: project.id });
      check(project.chapters.find(chapter => chapter.id === recoveryTarget.id).body === originalBody, "Recovery did not restore the chosen original body");
    } finally {
      releaseSave?.();
      await page.unroute("**/api/command");
      await page.evaluate(() => { window.__restoreDraftStorage?.(); delete window.__restoreDraftStorage; });
      const latest = await command("get_project", { project_id: project.id });
      await command("delete_project", { project_id: project.id, revision: latest.revision });
      await page.evaluate(async ({ original, deleted }) => {
        const drafts = await import("/src/features/writing/drafts.ts");
        const storage = await import("/src/app/storage.ts");
        drafts.clearProjectDrafts(deleted);
        storage.clearProjectStorage(deleted);
        localStorage.setItem("quillweave-project", original);
      }, { original: originalProjectId, deleted: project.id });
      await page.reload();
      await page.locator(".project-name").filter({ hasText: original.title }).click();
    }
}
