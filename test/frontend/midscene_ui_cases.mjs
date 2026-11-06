import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "playwright";
import { PlaywrightAgent } from "@midscene/web/playwright";

const cases = [];
let operations = 0;
const errors = [];
const browser = await chromium.launch({ channel: "chrome", headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, acceptDownloads: true });
const page = await context.newPage();
page.on("pageerror", (error) => errors.push(String(error)));
let agent;
let projectId = "";

/** Assert exact persisted expectations independently of the vision model's interpretation. */
function check(condition, description) {
  if (!condition) throw new Error(description);
}

/** Record each executed Midscene operation without retaining screenshots or model reasoning. */
async function visual(method, ...args) {
  operations++;
  console.log(`MIDSCENE ${method}: ${String(args[0]).slice(0, 180)}`);
  return agent[method](...args);
}

/** Read and prepare business fixtures through the application's actual command transport. */
async function command(name, args = {}) {
  return page.evaluate(async ({ name, args }) => {
    const response = await fetch("/api/command", { method: "POST", headers: { "Content-Type": "application/json", "X-Quillweave-Client": "1" }, body: JSON.stringify({ command: name, args: { ...args, ui_language: "zh-CN" } }) });
    const payload = await response.json();
    if (!payload.ok) throw new Error(payload.error);
    return payload.result;
  }, { name, args });
}

/** Wait on an observable business state rather than sleeping through possible failures. */
async function until(description, read, predicate) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const result = await read();
    if (predicate(result)) return result;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Business state did not settle: ${description}`);
}

/** Preserve case outcomes on both success and failure while letting real failures stop dependent scenarios. */
async function test(id, title, expected, body) {
  const record = { id, title, expected, status: "running", operations_before: operations };
  const start = Date.now();
  cases.push(record);
  console.log(`CASE ${id}: ${title}`);
  try {
    await body();
    record.status = "passed";
  } catch (error) {
    record.status = "failed";
    record.error = String(error.message || error).slice(0, 2500);
    throw error;
  } finally {
    record.seconds = (Date.now() - start) / 1000;
    record.midscene_operations = operations - record.operations_before;
    delete record.operations_before;
    await fs.writeFile("results.json", JSON.stringify({ cases, midscene_operations: operations }, null, 2));
    console.log(`CASE_RESULT ${JSON.stringify(record)}`);
  }
}

/** Enter and submit through visually located controls, then verify the real accepted job. */
async function send(text) {
  await visual("aiInput", "页面底部、发送按钮上方的写作请求多行输入框", { value: text, mode: "replace" });
  const response = page.waitForResponse((response) => response.url().endsWith("/api/command") && response.request().postDataJSON()?.command === "start_ai");
  await visual("aiKeyboardPress", undefined, { keyName: "Enter" });
  const payload = await (await response).json();
  check(payload.ok, payload.error || "Submission was rejected");
  return until("job completed", () => command("get_job", { job_id: payload.result.id }), (job) => {
    if (["error", "cancelled", "interrupted"].includes(job.status)) throw new Error(job.error || job.status);
    return job.status === "done";
  });
}

/** Prepare existing author-approved plans so visual testing can exercise their display and chapter creation. */
async function seedPlans() {
  let p = await command("get_project", { project_id: projectId });
  p = await command("save_plan", { project_id: projectId, revision: p.revision, plan: { level: "volume", title: "第一卷：北塔", start_chapter: 1, end_chapter: 10, target_characters: 20000, goal: "查明记忆交易的规则", status: "confirmed" } });
  const parent = p.studio.plans.find((plan) => plan.level === "volume").id;
  await command("save_plan", { project_id: projectId, revision: p.revision, plan: { parent_id: parent, level: "chapter", title: "第一章：铜钥匙", start_chapter: 1, end_chapter: 1, target_characters: 2000, goal: "交出铜钥匙并付出代价", conflict: "交出钥匙将失去一段记忆", status: "confirmed" } });
  await page.reload();
}

/** Prepare two truthful temporal versions for the visual history controls, without replacing their UI assertions. */
async function seedKnowledge() {
  let p = await command("get_project", { project_id: projectId });
  p = await command("save_memory", { project_id: projectId, revision: p.revision, memory: { kind: "knowledge", entity: "林舟", title: "钥匙位置知情者", content: "铜钥匙位于北塔。", chapter: 1, known_by: ["林舟"], status: "confirmed" } });
  const memory = p.studio.memories.find((m) => m.title === "钥匙位置知情者");
  await command("save_memory", { project_id: projectId, revision: p.revision, memory: { ...memory, chapter: 12, known_by: ["林舟", "岑月"] } });
  await page.reload();
}

/** Keep the existing complete studio scenarios available independently of a focused credential review. */
async function studioCases() {
  let first;
  await test("M01", "中文界面与新建故事", "视觉识别真实界面，切换中文并建立独立作品", async () => {
    await visual("aiAssert", "页面是 Quillweave 写作应用，能看到 English 语言选择和开始新故事的入口");
    await visual("aiAct", "打开右上角当前显示 English 的语言下拉框，选择中文");
    await visual("aiAct", "点击开始新故事，将暂定名称填写为“北塔借忆人 · Midscene 验收”，再点击开始找灵感");
    await visual("aiAssert", "当前已经进入名为“北塔借忆人 · Midscene 验收”的作品，界面文字为中文，可以看到共创讨论");
    const projects = await command("list_projects");
    check(projects.length === 1, "Story creation did not create exactly one work");
    projectId = projects[0].id;
  });
  await test("M02", "中文讨论、Enter 发送和作者采纳", "发送清空输入、不强推大纲，作者采纳后才写入共享决定", async () => {
    first = await send("我想写一个靠记忆交易生存的长篇故事。");
    await visual("aiAssert", "助手已经用中文讨论记忆交易，页面底部写作输入框为空，页面没有要求立刻写大纲的弹窗");
    check(await page.getByRole("textbox", { name: "写作请求", exact: true }).inputValue() === "", "Sent input was retained");
    check(await page.getByRole("dialog").count() === 0, "An unsolicited modal interrupted discussion");
    check(first.proposals.length === 1 && first.proposals[0].kind === "decision", "The model fixture did not stage the expected decision");
    await visual("aiAct", "在聊天消息区域内向下滚动，找到记忆交易的代价待审阅建议卡，点击这张卡片里的采纳按钮");
    const p = await until("decision adopted", () => command("get_project", { project_id: projectId }), (p) => p.studio.decisions.length === 1);
    check(p.studio.decisions[0].status === "accepted", "A proposal bypassed author acceptance");
  });
  await test("M03", "回退分支与相同文字的新发送", "原会话保留，同一新分支的相同文字两次发送产生不同任务", async () => {
    await visual("aiAct", "点击重命名会话按钮，将会话名称改成“北塔视觉讨论”，点击对话框中的保存");
    await visual("aiTap", "当前作者提问下方的回退到这里按钮");
    await visual("aiAssert", "写作输入框恢复了包含“记忆交易”的作者提问，可以修改后重新发送");
    const a = await send("请从岑月的角度继续讨论这个冲突。");
    const b = await send("请从岑月的角度继续讨论这个冲突。");
    check(a.id !== b.id && a.session_id === b.session_id && a.session_id !== first.session_id, "Acknowledged duplicate text reused an old task or branch");
    const original = await command("get_conversation", { project_id: projectId, session_id: first.session_id });
    check(original.turns.length === 2, "Rewind mutated original conversation history");
  });
  await test("M04", "作品形态与百万字章节目标", "连载、总字数、章节数和单章字数可统一保存", async () => {
    await visual("aiTap", "作品上方的创作目标按钮");
    await visual("aiAct", "在创作目标对话框里将作品形态选择为网文连载");
    await visual("aiInput", "创作目标对话框里的全书目标字数输入框", { value: "1000000" });
    await visual("aiInput", "创作目标对话框里的目标章节数输入框", { value: "500" });
    await visual("aiAct", "在创作目标对话框内滚动，使默认章节目标字数输入框可见");
    await visual("aiInput", "创作目标对话框里的默认章节目标字数输入框", { value: "2000" });
    await visual("aiAct", "在创作目标对话框内滚动到保存创作目标按钮，然后点击保存创作目标");
    const p = await until("goals saved", () => command("get_project", { project_id: projectId }), (p) => p.studio.target_chapters === 500);
    check(p.studio.mode === "serial" && p.studio.target_characters === 1000000 && p.default_chapter_words === 2000, "Goal values did not persist coherently");
    await visual("aiAssert", "作品头部显示网文连载、500章和1000000字的创作目标");
  });
  await test("M05", "已有分层规划与创建章节草稿", "已确认分卷与章计划层级可读，作者主动建立章节", async () => {
    await seedPlans();
    await visual("aiAct", "进入故事规划中的分层规划标签页");
    await visual("aiAssert", "分层规划中能看到第一卷：北塔和下级的第一章：铜钥匙，两条计划已经确认");
    await visual("aiTap", "分层规划工具栏里的建立章节草稿按钮");
    const p = await until("chapter created", () => command("get_project", { project_id: projectId }), (p) => p.chapters.length === 1);
    check(p.chapters[0].number === 1 && p.chapters[0].target_words === 2000, "The confirmed chapter plan did not produce its draft");
  });
  await test("M06", "章节正文保存", "视觉定位编辑器并保存准确的中文正文", async () => {
    await visual("aiTap", "左侧导航栏的章节写作按钮");
    const prose = "林舟把铜钥匙交给岑月。北塔的钟声随即响起，他再也想不起母亲的声音。";
    await visual("aiInput", "章节编辑区的大型章节正文输入框", { value: prose, mode: "replace" });
    await visual("aiTap", "章节编辑区上方的保存按钮");
    const p = await until("chapter saved", () => command("get_project", { project_id: projectId }), (p) => p.chapters[0].body === prose);
    check(p.chapters[0].body === prose, "The UI saved different prose");
    await visual("aiAssert", "章节编辑器显示林舟把铜钥匙交给岑月以及北塔钟声的中文正文");
  });
  await test("M07", "章后记忆的原文依据与采纳", "AI 只产生候选，作者采纳的记忆保存正确引文和来源", async () => {
    await visual("aiAct", "在写作助手区域打开AI任务下拉框，选择整理章节记忆");
    const job = await send("整理本章有原文依据的事实与人物变化，先作为建议让我审阅。");
    check(job.proposals.length === 1 && job.proposals[0].status === "proposed", "Memory update did not remain a proposal");
    await visual("aiAct", "在写作助手聊天消息区域内向下滚动，找到铜钥匙转移待审阅建议卡，点击这张卡片里的采纳按钮");
    const p = await until("memory adopted", () => command("get_project", { project_id: projectId }), (p) => p.studio.memories.length > 0);
    check(p.studio.memories.some((m) => m.source_id === p.chapters[0].id && m.quote === "林舟把铜钥匙交给岑月。"), "Memory quote or source did not survive adoption");
  });
  await test("M08", "人物知情范围的历史版本", "第6章只知林舟，第12章包含岑月，视图不混入未来状态", async () => {
    await seedKnowledge();
    await visual("aiAct", "进入左侧故事规划，再切换到故事记忆标签页");
    await visual("aiInput", "查看指定章节的记忆数字输入框", { value: "6" });
    await visual("aiAssert", "标题为“钥匙位置知情者”的记忆卡片中，知情人物只有林舟，没有岑月；请仅检查这张卡片");
    let card = page.locator(".memory-card").filter({ has: page.getByRole("heading", { name: "钥匙位置知情者", exact: true }) });
    check(await card.count() === 1 && !(await card.innerText()).includes("岑月"), "Early history leaked future knowledge");
    await visual("aiInput", "查看指定章节的记忆数字输入框", { value: "12" });
    await visual("aiAssert", "标题为“钥匙位置知情者”的记忆卡片中，知情人物包含林舟和岑月");
    check(await card.count() === 1 && (await card.innerText()).includes("岑月"), "Later history did not show its replacement version");
  });
  await test("M09", "研究来源与未发送的查询草稿", "搜索摘录可采纳，来源仍对应真正执行的查询", async () => {
    const view = await command("get_integrations");
    view.settings.search_enabled = true;
    view.settings.providers = [{ id: "fixture", name: "SearXNG", kind: "searxng", endpoint: process.env.QUILLWEAVE_TEST_MOCK, enabled: true }];
    await command("save_integrations", { settings: view.settings });
    await visual("aiTap", "工作台的资料研究标签页");
    await visual("aiInput", "联网资料研究区域的检索词输入框", { value: "明朝锦衣卫官制" });
    await visual("aiTap", "检索词右侧的搜索资料按钮");
    await page.getByRole("link", { name: "明代官制资料（验收夹具）", exact: true }).waitFor();
    await visual("aiAssert", "研究结果显示明代官制资料（验收夹具），并能看到来源网址和获取时间");
    await visual("aiInput", "联网资料研究区域的检索词输入框", { value: "唐朝官制：尚未发送的新查询" });
    await visual("aiTap", "已经显示的明代资料卡片中的采纳到资料库按钮");
    const p = await until("research adopted", () => command("get_project", { project_id: projectId }), (p) => p.sources.length > 0);
    check(p.sources.some((s) => s.url === "https://example.org/history" && s.content.includes("明朝锦衣卫官制")) && !p.sources.some((s) => s.content.includes("尚未发送的新查询")), "Research provenance drifted to the input draft");
  });
  await test("M10", "最小窗口与完整备份恢复", "发送按钮可见；视觉发起导出并恢复为一个独立副本", async () => {
    const originalBefore = await command("get_project", { project_id: projectId });
    const jobsBefore = await command("get_runs", { project_id: projectId });
    await visual("aiTap", "共创讨论标签页");
    await page.setViewportSize({ width: 860, height: 640 });
    await visual("aiAssert", "当前小窗口里仍然可以看见底部写作输入框和发送按钮，横向没有截掉主要输入区");
    const button = await page.locator(".assistant-composer").getByRole("button", { name: "发送", exact: true }).boundingBox();
    check(button && button.y + button.height <= 640 && await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "Minimum viewport clips its composer");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.evaluate(() => Object.defineProperty(window, "showSaveFilePicker", { value: undefined, configurable: true }));
    const downloading = page.waitForEvent("download");
    await visual("aiAct", "打开顶栏导出作品菜单，选择完整作品备份");
    const backup = await downloading;
    const backupPath = path.resolve("midscene-backup.quillweave.json");
    await backup.saveAs(backupPath);
    const content = await fs.readFile(backupPath, "utf8");
    check(!content.includes("provider_context") && !content.includes("execution_owner"), "Public backup leaked internal execution state");
    await visual("aiAct", "点击左侧我的作品，然后点击我的作品页顶部的导入按钮");
    const dialog = page.getByRole("dialog", { name: "导入作品", exact: true });
    await dialog.locator('input[type="file"]').setInputFiles(backupPath);
    await dialog.waitFor({ state: "detached" });
    const projects = await command("list_projects");
    check(projects.length === 2 && projects.some((p) => p.id === projectId) && projects.some((p) => p.id !== projectId), "Backup restoration did not retain the original and create exactly one independent copy");
    const restoredId = projects.find((p) => p.id !== projectId).id;
    const originalAfter = await command("get_project", { project_id: projectId });
    const restored = await command("get_project", { project_id: restoredId });
    check(JSON.stringify(originalAfter) === JSON.stringify(originalBefore), "Restoration modified the original work");
    for (const key of ["chapters", "sources"]) {
      check(JSON.stringify(restored[key]) === JSON.stringify(originalBefore[key]), `Restoration changed ${key}`);
    }
    check(restored.studio.memories.length === originalBefore.studio.memories.length && restored.studio.plans.length === originalBefore.studio.plans.length, "Restoration lost memory versions or plans");
    const restoredJobs = await command("get_runs", { project_id: restoredId });
    check(restoredJobs.length === jobsBefore.length && restoredJobs.every((job) => job.project_id === restoredId && !jobsBefore.some((old) => old.id === job.id)), "Restored task history was lost or reused original identities");
    await visual("aiTap", "左侧导航栏的我的作品按钮");
    await visual("aiAssert", "我的作品列表显示2部作品，有两行作品标题都为“北塔借忆人 · Midscene 验收”，每行都显示1章");
  });
}

/** Visually exercise blank, masked and cleared states using fake keys in the disposable application. */
async function credentialCase() {
  await test("K01", "密钥直接输入与遮蔽", "空白可直接填写，已配置只显示遮蔽，清除后恢复空白", async () => {
    await visual("aiAct", "打开右上角 English 语言选择，切换为中文，然后点击左侧模型设置");
    await visual("aiAct", "在模型设置页面向下滚动，直到当前模型的 API 密钥区域可见");
    await visual("aiAssert", "当前模型的 API 密钥标签下是空白输入框，没有显示保留当前密钥的操作下拉框");
    const input = page.getByLabel("API 密钥", { exact: true });
    check(await input.inputValue() === "" && !await input.getAttribute("placeholder") && await input.getAttribute("type") === "password", "The unconfigured key field is not blank and password-masked");
    await visual("aiInput", "当前模型的 API 密钥输入框", { value: "midscene-acceptance-fake-key", mode: "replace" });
    await visual("aiAct", "点击模型设置页面顶部的保存模型设置按钮");
    await until("model key saved", () => command("get_model_settings"), view => !!view.key_status[view.settings.profiles[0].id]);
    await visual("aiAct", "向下滚动，使当前模型的 API 密钥输入框可见");
    await visual("aiAssert", "API 密钥输入框中只显示遮蔽圆点，旁边有清除密钥按钮，没有操作下拉框");
    check(await input.inputValue() === "" && await input.getAttribute("placeholder") === "••••••••", "The saved key was exposed or lost its masking state");
    await visual("aiTap", "当前模型 API 密钥输入框右侧的清除密钥按钮");
    await visual("aiAssert", "当前模型的 API 密钥输入框已经恢复空白，没有遮蔽圆点，也没有保留当前密钥的操作下拉框");
    check(await input.inputValue() === "" && !await input.getAttribute("placeholder"), "Clear did not restore a blank field");
    await visual("aiAct", "点击模型设置页面顶部的保存模型设置按钮");
    await until("model key cleared", () => command("get_model_settings"), view => !view.key_status[view.settings.profiles[0].id]);
  });
}

/** Visually exercise real search CRUD, region isolation, OpenRouter execution and the minimum window. */
async function searchCases() {
  await test("S01", "搜索配置与主界面一致", "新连接默认关闭、空白密钥、真实可保存", async () => {
    await visual("aiAct", "打开右上角 English 语言选择，切换为中文，点击左侧模型设置，再点击页面上方的搜索配置页签");
    await visual("aiAssert", "页面显示搜索策略、启用联网搜索开关、默认搜索配置、搜索连接列表和添加搜索配置按钮，风格与主界面一致");
    await visual("aiTap", "添加搜索配置按钮");
    await visual("aiInput", "搜索配置弹窗里的搜索配置名称输入框", {value:"视觉搜索", mode:"replace"});
    await visual("aiAct", "在弹窗里将搜索服务选择为 Kimi，再将账户区域选择为国际 API 平台");
    check(await page.getByRole("dialog").getByRole("textbox", {name:"服务地址",exact:true}).inputValue()==="https://api.moonshot.ai/v1", "Kimi region not applied");
    await visual("aiAssert", "当前弹窗里的 API 密钥输入框为空白，没有保留当前密钥的操作下拉框");
    await visual("aiTap", "搜索配置弹窗里搜索服务字段的下拉框，当前为 Kimi");
    await visual("aiTap", "展开的搜索服务选项列表中的 OpenRouter 选项");
    check(await page.getByRole("dialog",{name:"搜索配置",exact:true}).count()===1,"Selecting a search service unexpectedly closed the dialog");
    await visual("aiInput", "搜索配置弹窗里的服务地址输入框", {value:process.env.QUILLWEAVE_TEST_MOCK+"/v1",mode:"replace"});
    await visual("aiTap", "搜索配置弹窗里的研究模型连接下拉框");
    await visual("aiTap", "展开的研究模型选项列表中的本地创作响应夹具选项");
    await visual("aiInput", "搜索配置弹窗里的 API 密钥输入框", {value:"midscene-search-fake-key",mode:"replace"});
    await visual("aiAct", "在弹窗内向下滚动找到启用此搜索配置开关，打开它，然后点击底部保存搜索配置");
    const view=await command("get_search_settings");
    const profile=view.settings.profiles.find(p=>p.name==="视觉搜索");
    check(profile?.family==="openrouter"&&profile.enabled&&view.key_status[profile.id],"Visual profile did not save");
    check(!JSON.stringify(view).includes("midscene-search-fake-key"),"Profile readback leaked a key");
  });
  await test("S02", "原生联网与引用用量", "实际经过 OpenRouter 适配，显示来源引用与报告费用", async () => {
    await visual("aiAct", "将搜索策略里的启用联网搜索开关打开");
    await until("global enabled",()=>command("get_search_settings"),view=>view.settings.enabled);
    await visual("aiAct", "向下滚动页面，让搜索连接列表上方的搜索配置名称筛选输入框和服务筛选完整出现在可见区域");
    await visual("aiInput", "搜索连接列表上方的筛选搜索配置输入框", {value:"视觉搜索",mode:"replace"});
    await visual("aiTap", "视觉搜索这一行的测试搜索按钮");
    await until("native completed",async()=>{const view=await command("get_search_settings");return command("get_search_profile",{profile_id:view.settings.profiles.find(p=>p.name==="视觉搜索").id})},info=>info.last_test?.status==="succeeded");
    await visual("aiAct", "向下滚动到搜索运行状态和研究模型回答区域");
    await visual("aiAssert", "搜索已完成，测试结论说明搜索证据已核验，显示有引用的中文研究回答、资料来源链接、实际引擎 exa、模型输入输出词元和供应商报告费用");
    const view=await command("get_search_settings");const info=await command("get_search_profile",{profile_id:view.settings.profiles.find(p=>p.name==="视觉搜索").id});
    check(info.last_test.response.search_state==="executed"&&info.last_test.response.answer.citations.length===1&&info.last_test.response.usage.reported_cost===.001,"Native evidence or usage was lost");
  });
  await test("S03", "小窗口与全局关闭", "860×640 控件不溢出，全局关闭可以保存", async () => {
    await page.setViewportSize({width:860,height:640});
    await visual("aiAct", "向上滚动到搜索策略区域，关闭启用联网搜索开关");
    await until("search disabled",()=>command("get_search_settings"),view=>!view.settings.enabled);
    await visual("aiAssert", "小窗口中当前可见的搜索设置文字可阅读，搜索策略开关已关闭，没有横向溢出");
    await visual("aiAct", "向下滚动，直到搜索策略的保存搜索策略和重新加载已保存策略两个按钮完整可见，不点击它们");
    await visual("aiAssert", "保存搜索策略和重新加载已保存策略两个操作按钮完整可见，没有互相重叠或被遮挡");
    check(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"Minimum window overflow");
  });
}

try {
  await page.goto(process.env.QUILLWEAVE_TEST_URL);
  agent = new PlaywrightAgent(page, { cache: false, generateReport: true, reportFileName: "quillweave", replanningCycleLimit: 15 });
  if (process.env.QUILLWEAVE_SEARCH_ONLY === "1") await searchCases();
  else if (process.env.QUILLWEAVE_CREDENTIALS_ONLY === "1") await credentialCase();
  else await studioCases();
  check(errors.length === 0, errors.join("\n"));
  console.log(`MIDSCENE_ACCEPTANCE_PASSED ${cases.length} cases, ${operations} SDK operations`);
} catch (error) {
  await fs.writeFile("failure-context.json", JSON.stringify({
    error: String(error.message || error),
    visible_text: (await page.locator("body").innerText()).slice(0, 14000),
    tasks: projectId ? (await command("get_runs", { project_id: projectId })).map((job) => ({ id: job.id, action: job.action, status: job.status, error: job.error, proposals: job.proposals?.map((p) => ({ title: p.title, status: p.status })) })) : [],
  }, null, 2));
  await page.screenshot({ path: "failure.png" });
  console.error("MIDSCENE_ACCEPTANCE_FAILED", String(error.message || error));
  process.exitCode = 1;
} finally {
  await fs.writeFile("results.json", JSON.stringify({ cases, midscene_operations: operations, page_errors: errors }, null, 2));
  try { await agent?.destroy(); } finally { await browser.close(); }
}
