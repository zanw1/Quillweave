/** Exercise search settings through real rendered controls, keeping HTTP suppliers deterministic and disposable. */
async page => {
  const mock = "__STUDIO_MOCK_URL__";
  const artifacts = __STUDIO_ARTIFACT_DIR_JSON__;
  const checks = [], errors = [];
  page.on("pageerror", error => errors.push(String(error)));
  /** Fail on a falsifiable product expectation, independent of the text returned by an AI model. */
  function check(condition, detail) { if (!condition) throw new Error(detail); }
  /** Use the same command transport as the application for state and fixture-only setup. */
  async function command(name, args = {}) {
    return page.evaluate(async ({ name, args }) => {
      const r = await fetch("/api/command", { method:"POST", headers:{"Content-Type":"application/json","X-Quillweave-Client":"1"}, body:JSON.stringify({command:name,args:{...args,ui_language:"zh-CN"}}) });
      const envelope = await r.json(); if (!envelope.ok) throw new Error(envelope.error); return envelope.result;
    }, {name,args});
  }
  /** Select via the shared accessible component, including its portalled menu. */
  async function choose(scope, label, option) {
    await scope.getByRole("combobox", {name:label,exact:true}).click();
    await page.getByRole("option", {name:option,exact:true}).click();
  }
  /** Observe successful persistence before inspecting either the next screen or its credential state. */
  async function save(scope, button, operation="mutate_search_settings") {
    const response=page.waitForResponse(r=>r.url().endsWith("/api/command")&&r.request().postDataJSON()?.command===operation);
    await scope.getByRole("button",{name:button,exact:true}).click();
    const r=await response, envelope=await r.json();check(envelope.ok,envelope.error||"save rejected");return {result:envelope.result,args:r.request().postDataJSON().args};
  }
  /** Filter before editing to exercise pagination without depending on preset ordering. */
  async function edit(name) {
    await page.getByRole("textbox",{name:"筛选搜索配置",exact:true}).fill(name);
    await page.locator("tbody tr").filter({hasText:name}).getByRole("button",{name:"编辑",exact:true}).click();
    return page.getByRole("dialog",{name:"搜索配置",exact:true});
  }
  /** Save the current dialog and wait for its native modal to release focus. */
  async function saveEditor(dialog) {
    const result=await save(dialog,"保存搜索配置");await dialog.waitFor({state:"detached"});return result;
  }

  await page.setViewportSize({width:1440,height:900});
  await choose(page,"Interface language","中文");
  await page.getByRole("button",{name:"开始新故事",exact:true}).click();
  await page.getByRole("textbox",{name:"暂定名称（可选）",exact:true}).fill("搜索闭环验收");
  await page.getByRole("button",{name:"开始找灵感",exact:true}).click();
  const projectId=(await command("list_projects"))[0].id;
  await page.getByRole("button",{name:"模型设置",exact:true}).click();
  await page.getByRole("button",{name:"搜索配置",exact:true}).click();
  await page.getByRole("heading",{name:"搜索连接",exact:true}).waitFor();
  check(!(await command("get_search_settings")).settings.profiles.filter(p=>["deepseek","kimi","openrouter"].includes(p.family)).some(p=>p.enabled),"New paid profiles enabled automatically");

  await page.getByRole("button",{name:"添加搜索配置",exact:true}).click();
  let dialog=page.getByRole("dialog",{name:"搜索配置",exact:true});
  const key=dialog.getByLabel("API 密钥",{exact:true});
  check(await key.inputValue()==="" && !await key.getAttribute("placeholder") && await key.getAttribute("type")==="password","Missing key did not appear blank and masked");
  await dialog.getByRole("textbox",{name:"搜索配置名称",exact:true}).fill("验收 DeepSeek");
  await dialog.getByRole("textbox",{name:"服务地址",exact:true}).fill(mock);
  await key.fill("search-ui-fake-key");
  await dialog.getByRole("switch",{name:"启用此搜索配置",exact:true}).check();
  const first=await saveEditor(dialog), profileId=first.args.profile.id;
  check(first.args.key_updates[profileId].value==="search-ui-fake-key"&&!JSON.stringify(first.result).includes("search-ui-fake-key"),"Key contract violated");
  dialog=await edit("验收 DeepSeek");
  check(await dialog.getByLabel("API 密钥",{exact:true}).inputValue()===""&&await dialog.getByLabel("API 密钥",{exact:true}).getAttribute("placeholder")==="••••••••","Saved secret was not mask-only");
  await choose(dialog,"密钥来源","复用同服务模型的密钥");
  await choose(dialog,"研究模型连接","本地验收模型 · studio-fixture");
  await choose(dialog,"密钥来源","填写独立密钥");
  await dialog.getByLabel("API 密钥",{exact:true}).fill("search-ui-fake-key");
  const independent=await saveEditor(dialog);
  check(!independent.args.profile.model_profile_id,"Switching to an independent key left a hidden model reference");
  checks.push("S01: profile CRUD, blank key, exact replace, redacted response and mask-only reopen");

  await page.getByRole("button",{name:"添加搜索配置",exact:true}).click();
  dialog=page.getByRole("dialog",{name:"搜索配置",exact:true});
  await choose(dialog,"搜索服务","Kimi");
  await dialog.getByRole("textbox",{name:"搜索配置名称",exact:true}).fill("验收 Kimi");
  await dialog.getByLabel("API 密钥",{exact:true}).fill("discard-region-key");
  await choose(dialog,"账户区域","国际 API 平台");
  check(await dialog.getByRole("textbox",{name:"服务地址",exact:true}).inputValue()==="https://api.moonshot.ai/v1"&&await dialog.getByLabel("API 密钥",{exact:true}).inputValue()==="","Region change reused another account key");
  await choose(dialog,"搜索模式","Pro · 来源段落");
  await dialog.getByRole("textbox",{name:"服务地址",exact:true}).fill(mock+"/v1");
  await dialog.getByRole("textbox",{name:"限定网站",exact:true}).fill("example.org");
  await dialog.getByLabel("发布时间起点",{exact:true}).fill("2020-01-01");
  await dialog.getByLabel("API 密钥",{exact:true}).fill("kimi-ui-fake-key");
  await dialog.getByRole("switch",{name:"启用此搜索配置",exact:true}).check();
  const kimi=await saveEditor(dialog);
  check(kimi.args.profile.mode==="pro"&&kimi.args.profile.region==="global"&&kimi.args.profile.filters.allowed_domains[0]==="example.org","Kimi fields not persisted");
  checks.push("S02: Kimi region isolation; S03: Pro site/date capability and persistence");

  await page.getByRole("switch",{name:"启用联网搜索",exact:true}).check();
  await choose(page,"默认搜索配置","验收 DeepSeek");
  await choose(page,"备用策略","暂时失败时按顺序切换");
  await choose(page,"添加备用搜索配置","验收 Kimi");
  await save(page,"保存搜索策略");
  let view=await command("get_search_settings");
  check(view.settings.default_profile_id===profileId&&view.settings.fallback_profile_ids[0]===kimi.args.profile.id,"Policy did not persist");
  await page.locator("tbody tr").filter({hasText:"验收 DeepSeek"}).getByRole("button",{name:/删除搜索配置/}).click();
  const deletion=page.getByRole("dialog",{name:"删除搜索配置",exact:true});
  await deletion.getByRole("button",{name:"删除",exact:true}).click();
  await page.getByRole("alert").filter({hasText:"默认或备用"}).waitFor();
  await deletion.getByRole("button",{name:"取消",exact:true}).click();
  checks.push("S04: default/fallback ordering and reference-protected delete");

  let lostId="";
  /** Lose only the acknowledgement after the actual server has durably accepted the test. */
  const loseAck=async route=>{
    if(route.request().postDataJSON()?.command==="start_search_run"&&!lostId){const response=await route.fetch();const e=await response.json();check(e.ok,e.error);lostId=e.result.id;await route.abort("failed");}
    else await route.continue();
  };
  await page.route("**/api/command",loseAck);
  const row=page.locator("tbody tr").filter({hasText:"验收 DeepSeek"});
  await row.getByRole("button",{name:"测试搜索",exact:true}).click();
  await page.getByRole("alert").filter({hasText:/fetch|加载|网络|Failed/i}).waitFor();
  await row.getByRole("button",{name:"测试搜索",exact:true}).click();
  await page.getByText("搜索证据已核验",{exact:false}).waitFor();
  await page.unroute("**/api/command",loseAck);
  check(lostId&&(await command("get_search_run",{run_id:lostId})).status==="succeeded","Lost ack did not reuse accepted run");
  checks.push("S05: real search test, evidence, lost-ack idempotent retry");

  await page.locator(".tabs").getByRole("button",{name:/^模型配置/}).click();
  await choose(page,"此模型的联网搜索","禁止联网搜索");
  await save(page,"保存搜索关联");
  view=await command("get_search_settings");
  check(Object.values(view.settings.bindings).some(binding=>binding.mode==="off"),"Model binding did not persist");
  checks.push("S06: independent model search binding");

  await page.getByRole("button",{name:"我的作品",exact:true}).click();
  await page.getByText("搜索闭环验收",{exact:true}).click();
  await page.getByRole("button",{name:"资料研究",exact:true}).click();
  await choose(page,"本次请求的搜索配置","验收 DeepSeek");
  await page.getByRole("textbox",{name:"检索词",exact:true}).fill("明代官制真实执行问题");
  await page.getByRole("button",{name:"搜索资料",exact:true}).click();
  await page.getByRole("link",{name:"明代官制资料（验收夹具）",exact:true}).waitFor();
  await page.getByRole("textbox",{name:"检索词",exact:true}).fill("尚未发送的新查询");
  await page.getByRole("button",{name:"采纳到资料库",exact:true}).click();
  await page.getByRole("button",{name:"已保存",exact:true}).waitFor();
  const p=await command("get_project",{project_id:projectId});
  check(p.sources.length===1&&p.sources[0].provenance.query==="明代官制真实执行问题"&&p.sources[0].provenance.run_id,"Adoption lost executed query provenance");
  await page.getByRole("button",{name:"读取原文",exact:true}).click();
  await page.getByText("已读取网页内容",{exact:true}).waitFor();
  checks.push("S07: once-choice overrides model off; S08: source adoption, query snapshot and fetch");

  view=await command("get_search_settings");
  await command("mutate_search_settings",{operation:"set_binding",expected_revision:view.revision,model_revision:view.model_revision,model_profile_id:view.models[0].id,binding:{mode:"inherit"}});
  await choose(page,"本次请求的搜索配置","继承模型关联与默认搜索配置");
  await page.getByRole("textbox",{name:"检索词",exact:true}).fill("需要备用");
  await page.getByRole("button",{name:"搜索资料",exact:true}).click();
  await page.getByText("供应商选取的段落",{exact:true}).waitFor();
  const fetched=page.waitForResponse(r=>r.url().endsWith("/api/command")&&r.request().postDataJSON()?.command==="start_search_run");
  await page.getByRole("button",{name:"读取原文",exact:true}).click();
  const fetchRequest=(await fetched).request().postDataJSON().args;
  check(fetchRequest.operation==="fetch"&&fetchRequest.selection.profile_id===kimi.args.profile.id,"Reading a fallback source incorrectly selected the primary supplier");
  await page.getByText("已读取网页内容",{exact:true}).waitFor();
  checks.push("S08b: primary outage uses authorized Kimi fallback; page fetch retains actual successful supplier");

  await page.getByRole("button",{name:"搜索设置",exact:true}).click();
  const parent=page.getByRole("dialog",{name:"搜索设置",exact:true});
  await parent.getByRole("switch",{name:"启用联网搜索",exact:true}).uncheck();
  await parent.getByRole("button",{name:"关闭对话框",exact:true}).click();
  check(await page.getByRole("button",{name:"搜索资料",exact:true}).isDisabled(),"Global off did not override explicit profile choice");
  await page.reload();
  if (!await page.getByRole("button",{name:"资料研究",exact:true}).count()) await page.locator("tbody tr").getByText("搜索闭环验收",{exact:true}).click();
  await page.getByRole("button",{name:"资料研究",exact:true}).click();
  await page.getByText("已读取网页内容",{exact:true}).waitFor();
  checks.push("S09: global hard-off and restored durable result after reload");

  await page.getByRole("button",{name:"搜索设置",exact:true}).click();
  await parent.getByRole("textbox",{name:"筛选搜索配置",exact:true}).fill("验收 Kimi");
  await parent.locator("tbody tr").filter({hasText:"验收 Kimi"}).getByRole("button",{name:"编辑",exact:true}).click();
  dialog=page.getByRole("dialog",{name:"搜索配置",exact:true});
  await page.setViewportSize({width:860,height:640});
  await dialog.getByRole("button",{name:"保存搜索配置",exact:true}).scrollIntoViewIfNeeded();
  const geometry=await dialog.locator('input:not([type="checkbox"]), .qw-select-trigger').evaluateAll(elements=>elements.filter(e=>e.getBoundingClientRect().height>0).map(e=>({height:e.getBoundingClientRect().height,font:getComputedStyle(e).fontSize,radius:getComputedStyle(e).borderRadius})));
  check(geometry.length>10&&geometry.every(f=>f.height===40&&f.font==="13px"&&f.radius==="8px"),"Search controls diverged from the main UI");
  check(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"Search settings overflow the minimum window");
  await page.screenshot({path:`${artifacts}/search-minimum-window.png`});
  await dialog.getByRole("button",{name:"取消",exact:true}).click();
  await parent.getByRole("button",{name:"关闭对话框",exact:true}).click();
  check(errors.length===0,errors.join("\n"));
  checks.push("S10: nested modal, accessible controls, 860×640, shared 40px/13px/8px geometry");
  return {result:"PASS: search settings and research",checks,geometry};
}
