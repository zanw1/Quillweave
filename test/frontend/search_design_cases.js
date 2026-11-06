/** Validate the search-design prototype's observable interactions without calling production services. */
async page => {
  const checks = [];
  const errors = [];
  let businessRequests = 0;
  page.on("pageerror", error => errors.push(String(error)));
  page.on("request", request => { if (request.url().includes("/api/command")) businessRequests++; });
  await page.route("**/api/**", route => route.abort());
  const artifacts = __DESIGN_ARTIFACTS__;

  /** Fail on an exact user-facing contract instead of silently skipping an unavailable control. */
  function check(condition, message) { if (!condition) throw new Error(message); }

  /** Use the shared accessible searchable selector inside and outside dialogs. */
  async function choose(scope, label, option) {
    await scope.getByRole("combobox", { name: label, exact: true }).click();
    await page.getByRole("option", { name: option, exact: true }).click();
  }

  /** Open an existing named configuration from the current table. */
  async function edit(name) {
    await page.locator(".prototype-table tbody tr").filter({ hasText: name }).getByRole("button", { name: "编辑", exact: true }).click();
    return page.getByRole("dialog", { name: "搜索配置", exact: true });
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  try {
  await page.getByRole("heading", { name: "模型配置与搜索服务", exact: true }).waitFor();
  check(await page.getByRole("button", { name: "演示搜索", exact: true }).isDisabled(), "Global off did not block the preview");
  check(await page.locator(".prototype-table tbody tr").count() === 5, "Main configuration listing is not paged");
  await page.getByRole("button", { name: "下一页", exact: true }).click();
  check(await page.locator(".prototype-table tbody tr").count() === 4, "Pagination did not show the remainder");
  await page.getByRole("button", { name: "上一页", exact: true }).click();
  checks.push("global-off authority and real example pagination");

  let dialog = await edit("DeepSeek 搜索");
  let key = dialog.getByLabel("API 密钥", { exact: true });
  check(await key.inputValue() === "" && !await key.getAttribute("placeholder"), "New credential field is not blank");
  await key.fill("design-acceptance-fake");
  await dialog.getByRole("button", { name: "保存搜索配置", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  dialog = await edit("DeepSeek 搜索");
  key = dialog.getByLabel("API 密钥", { exact: true });
  check(await key.inputValue() === "" && await key.getAttribute("placeholder") === "••••••••", "Configured example leaked a key or lost masking");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  checks.push("blank and retained masked keys; replacement text discarded after example save");

  await page.getByRole("button", { name: "新增搜索配置", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "搜索配置", exact: true });
  await choose(dialog, "搜索服务", "Kimi");
  await dialog.getByLabel("配置名称", { exact: true }).fill("朝代资料检索");
  await choose(dialog, "接入方式", "Pro · 正文片段");
  await choose(dialog, "账户区域", "国际开放平台");
  check(await dialog.getByLabel("服务地址", { exact: true }).inputValue() === "https://api.moonshot.ai/v1", "Region did not select the matching endpoint");
  check(!await dialog.getByLabel("API 密钥", { exact: true }).getAttribute("placeholder"), "Region carried a previous key into a new destination");
  await dialog.getByLabel("每次结果数量", { exact: true }).fill("21");
  await dialog.getByRole("button", { name: "保存搜索配置", exact: true }).click();
  check(await dialog.getByLabel("每次结果数量", { exact: true }).evaluate(input => input.validity.rangeOverflow), "Kimi result bound was not enforced");
  await dialog.getByLabel("每次结果数量", { exact: true }).fill("5");
  await dialog.getByLabel("指定站点", { exact: true }).fill("a.org,b.org,c.org,d.org,e.org,f.org");
  await dialog.getByRole("button", { name: "保存搜索配置", exact: true }).click();
  await dialog.getByRole("alert").filter({ hasText: "最多允许 5" }).waitFor();
  await dialog.getByLabel("指定站点", { exact: true }).fill("example.org");
  await dialog.getByLabel("API 密钥", { exact: true }).fill("design-fake-kimi");
  await dialog.getByRole("switch", { name: "启用此搜索配置", exact: true }).check();
  await dialog.getByRole("button", { name: "保存搜索配置", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  checks.push("Kimi region/preset, result and domain validation, editable provider-specific configuration");

  await choose(page, "筛选服务商", "Kimi");
  check(await page.locator(".prototype-table tbody tr").count() === 2, "Provider filtering is ineffective");
  await page.getByLabel("搜索配置名称", { exact: true }).fill("朝代资料");
  check(await page.locator(".prototype-table tbody tr").count() === 1, "Name filtering is ineffective");
  await page.getByLabel("搜索配置名称", { exact: true }).fill("");
  await choose(page, "筛选服务商", "全部服务商");
  await page.getByRole("button", { name: "删除DeepSeek 搜索", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "仍被默认" }).waitFor();
  checks.push("filters reset page and active configuration references block deletion");

  await page.getByRole("switch", { name: "启用联网搜索", exact: true }).check();
  await choose(page, "备用搜索配置", "Kimi 搜索");
  await choose(page, "备用搜索配置", "朝代资料检索");
  await page.getByRole("button", { name: "上移备用2", exact: true }).click();
  check((await page.getByRole("list", { name: "备用顺序", exact: true }).innerText()).indexOf("朝代资料检索") < (await page.getByRole("list", { name: "备用顺序", exact: true }).innerText()).indexOf("Kimi 搜索"), "Ordered fallback controls did not reorder");
  await choose(page, "全局默认搜索", "Kimi 搜索");
  check(!(await page.getByRole("list", { name: "备用顺序", exact: true }).innerText()).includes("Kimi 搜索"), "A primary configuration also remains in fallback");
  checks.push("fallback opt-in, ordering, and primary/fallback exclusion");

  await page.getByRole("tab", { name: "模型配置", exact: true }).click();
  await choose(page, "DeepSeek 写作模型（示例） 搜索配置", "关闭联网");
  check(await page.getByRole("button", { name: "演示搜索", exact: true }).isDisabled(), "Explicit model off fell through to global default");
  await choose(page, "本次搜索配置", "Kimi 搜索");
  check(await page.getByRole("button", { name: "演示搜索", exact: true }).isEnabled(), "Explicit per-request selection did not override the model binding");
  await page.getByRole("tab", { name: "搜索配置", exact: true }).click();
  checks.push("explicit off and request/model/global selection precedence");

  await page.getByRole("button", { name: "新增搜索配置", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "搜索配置", exact: true });
  await choose(dialog, "搜索服务", "OpenRouter");
  await choose(dialog, "搜索引擎", "上游原生搜索");
  await dialog.getByRole("status").filter({ hasText: "不能保证硬次数" }).waitFor();
  check(await dialog.getByLabel("每次结果数量", { exact: true }).isDisabled(), "Unsupported native result count remains editable");
  await page.setViewportSize({ width: 860, height: 640 });
  const fieldMetrics = await dialog.locator('input:not([type="checkbox"]), .qw-select-trigger').evaluateAll(elements => elements.map(element => ({ height:element.getBoundingClientRect().height, font:getComputedStyle(element).fontSize, radius:getComputedStyle(element).borderRadius })));
  check(fieldMetrics.every(field => field.height === 40 && field.font === "13px" && field.radius === "8px"), "Prototype fields diverged from shared density");
  await dialog.getByRole("combobox", { name: "搜索引擎", exact: true }).click();
  await page.keyboard.press("Escape");
  check(await dialog.isVisible(), "Selector Escape also dismissed the dialog");
  const saveBox = await dialog.getByRole("button", { name: "保存搜索配置", exact: true }).boundingBox();
  check(saveBox && saveBox.y >= 0 && saveBox.y + saveBox.height <= 640, "Minimum-window dialog hides its save action");
  await dialog.locator(".prototype-editor-fields").evaluate(element => { element.scrollTop = 0; });
  await page.screenshot({ path: `${artifacts}/editor-860.png`, animations: "disabled" });
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "Minimum window overflows horizontally");
  await page.setViewportSize({ width: 1440, height: 900 });
  checks.push("OpenRouter capability warning, disabled unsupported input, shared geometry and modal Escape at 860x640");

  const search = page.getByLabel("搜索内容", { exact: true });
  await search.fill("先检索的明代官制");
  await page.getByRole("button", { name: "演示搜索", exact: true }).click();
  await search.fill("尚未发送的新查询");
  await page.locator(".prototype-source").waitFor();
  check((await page.locator(".prototype-source").innerText()).includes("先检索的明代官制") && !(await page.locator(".prototype-source").innerText()).includes("尚未发送的新查询"), "Result provenance drifted to an unsent input");
  await page.getByRole("button", { name: "采纳到示例资料库", exact: true }).click();
  await page.getByRole("button", { name: "已采纳到示例资料库", exact: true }).waitFor();
  checks.push("executed-query snapshot and explicit source adoption");

  await page.clock.install();
  await page.getByRole("button", { name: "演示搜索", exact: true }).click();
  await page.getByRole("button", { name: "取消演示", exact: true }).click();
  await page.clock.runFor(500);
  check(await page.locator(".prototype-source").count() === 0, "A late result overwrote cancellation");
  await page.clock.resume();
  await page.getByRole("switch", { name: "启用联网搜索", exact: true }).uncheck();
  check(await page.getByRole("button", { name: "演示搜索", exact: true }).isDisabled(), "Global off was bypassed by a per-request profile");
  checks.push("cancelled work rejects late completion and global off overrides explicit selection");
  check(businessRequests === 0 && errors.length === 0, "The prototype reached production or raised an uncaught page error");
  checks.push("isolated prototype: zero business HTTP calls and no page errors");
  await page.locator(".prototype-content").evaluate(element => { element.scrollTop = 0; });
  await page.screenshot({ path: `${artifacts}/settings-1440.png`, animations: "disabled" });
  return { result: "passed", checks, fieldMetrics, businessRequests, pageErrors: errors };
  } catch (error) {
    await page.screenshot({ path: `${artifacts}/failure.png` });
    throw error;
  }
}
