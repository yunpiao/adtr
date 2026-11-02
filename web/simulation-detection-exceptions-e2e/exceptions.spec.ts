import { expect, test, type Page } from "@playwright/test";
import { mkdir, readFile } from "node:fs/promises";
import { resolve } from "node:path";
test.use({ trace: "off", screenshot: "off", video: "off", launchOptions: {
  env: Object.fromEntries(Object.entries(process.env).filter(
    (e): e is [string,string] => e[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(e[0]),
  )),
} });
const api = "/api/simulation/detection-exceptions";
const detection = "/api/simulation/detection";
const evidence = resolve("../artifacts/simulation-detection-exceptions");
async function login(page: Page, username: string, password: string) {
  await page.goto("/");
  await page.getByLabel("用户名", { exact:true }).fill(username);
  await page.getByLabel("密码", { exact:true }).fill(password);
  await page.getByRole("button", { name:"登录", exact:true }).click();
  await expect(page.getByRole("heading", {name:"登录账户",exact:true})).toBeHidden();
  const response = await page.request.get("/api/auth/me");
  expect(response.status()).toBe(200);
  return response.json();
}
async function open(page: Page) {
  await page.goto("/#simulation-detection-exceptions");
  await page.getByRole("radio", {name:"选择数据源 simulation.example.test",exact:true}).check();
  await page.getByRole("button", {name:"核对并打开连接详情",exact:true}).click();
  await expect(page.getByRole("button",{name:"新增禁用草稿",exact:true})).toBeEnabled();
}
async function action(page: Page, route: string, label: string) {
  const pending = page.waitForResponse(r => r.url().endsWith(api+route) && r.request().method()==="POST");
  await page.getByRole("button",{name:label,exact:true}).click();
  const response = await pending;
  expect(response.status()).toBe(200);
  const receipt = await response.json(), input = response.request().postDataJSON();
  expect(receipt.action).toBe(route.slice(1));
  expect(receipt.requestHash).toMatch(/^[0-9a-f]{64}$/);
  expect(receipt.intent).toEqual({domainId:input.domainId,action:route.slice(1),operationId:input.operationId,
    expectedRevision:input.expectedRevision,id:input.id??"",declaration:input.declaration??null,
    entries:input.entries??null,sourceTaskId:input.sourceTaskId??""});
  await expect(page.getByLabel("已确认回执",{exact:true})).toContainText(receipt.operationId);
  await expect(page.getByText(`SIMULATED · 域 ${input.domainId} · 当前修订 ${receipt.revision}`,{exact:true})).toBeVisible();
  return receipt;
}
async function safeResultScreenshot(page:Page, filename:string) {
  const region=page.getByLabel("评估结果",{exact:true});
  await region.scrollIntoViewIfNeeded();
  await region.evaluate(el=>el.scrollIntoView({block:"center",behavior:"instant"}));
  const box=await region.boundingBox();
  const header=await page.locator(".soc-topbar").boundingBox();
  expect(box).not.toBeNull();
  expect(box!.y).toBeGreaterThanOrEqual(header ? header.y+header.height : 0);
  expect(box!.y+box!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
  await expect(region).toContainText("SIMULATED");
  await region.screenshot({path:resolve(evidence,filename)});
}
test("SIMULATED exceptions preserve actual Worker matches, recover receipts and immutable history",async({page})=>{
  const username=process.env.ADTR_E2E_USERNAME,password=process.env.ADTR_E2E_PASSWORD;
  if(!username||!password) throw new Error("Dedicated disposable harness required");
  await mkdir(evidence,{recursive:true});
  const actor=await login(page,username,password),domainId="simulation-domain-a";
  // APIRequestContext does not add the browser Origin header automatically.
  const origin = new URL(page.url()).origin;
  async function post(path:string,body:unknown){
    const response=await page.request.post(path,{data:body,headers:{Origin:origin,"X-CSRF-Token":actor.csrfToken}});
    expect(response.status()).toBe(200); return response.json();
  }
  const template=(await post(detection+"/create",{domainId,templateId:"browser-template",name:"SIMULATED browser source",operationId:"browser-template-create",rules:[{id:"browser-rule",enabled:true,combine:"all",conditions:[{field:"event.outcome",operator:"eq",values:["failure"]}]}]})).template;
  const submission=await post(detection+"/run",{domainId,templateId:template.id,templateVersion:1,datasetId:"simulation-dataset",operationId:"browser-run"});
  const taskId=submission.submission.task.taskUUID,taskURL=detection+"/task?domainId="+domainId+"&taskUUID="+taskId;
  await expect.poll(async()=>{const r=await page.request.get(taskURL);expect(r.status()).toBe(200);return(await r.json()).task.state;},{timeout:20_000}).toBe("succeeded");
  const completed=await(await page.request.get(taskURL)).json(),source=completed.task.result;
  expect(completed.task.attempt).toBe(1);
  expect(source.eventCount).toBe(2);
  expect(source.matches.map((m:{rule_id:string,event_id:string})=>[m.rule_id,m.event_id])).toEqual([["browser-rule","exception-positive"]]);
  expect(source.matches.filter((m:{event_id:string})=>m.event_id==="exception-negative")).toHaveLength(0);
  await open(page);
  await page.getByRole("button",{name:"新增禁用草稿",exact:true}).click();
  await page.getByLabel("例外 ID",{exact:true}).fill("browser-exception");
  await page.getByLabel("理由",{exact:true}).fill("SIMULATED browser confirmed source");
  await page.getByLabel("检测任务 UUID",{exact:true}).fill(taskId);
  await page.getByLabel("来源事件 ID",{exact:true}).fill("exception-positive");
  await page.getByLabel("来源规则 ID",{exact:true}).fill("browser-rule");
  const previewResponse=page.waitForResponse(r=>r.url().endsWith(api+"/preview"));
  await page.getByRole("button",{name:"从真实命中预览禁用声明",exact:true}).click();
  expect((await previewResponse).status()).toBe(200);
  await expect(page.getByLabel("规则来源任务 UUID",{exact:true})).toHaveValue(taskId);
  let createPosts=0;
  await page.route(`**${api}/create`,async route=>{createPosts++;const r=await route.fetch();expect(r.status()).toBe(200);await route.abort("failed");},{times:1});
  await page.getByRole("button",{name:"保存禁用草稿",exact:true}).click();
  await page.getByRole("button",{name:"读取原回执",exact:true}).click();
  await expect(page.getByLabel("已确认回执",{exact:true})).toContainText("create");
  expect(createPosts).toBe(1);
  await page.getByRole("button",{name:/^browser-exception ·/}).click();
  await page.getByLabel("评估记录 ID",{exact:true}).fill("browser-disabled");
  let receipt=await action(page,"/evaluate","按当前修订显式评估");
  expect(receipt.evaluation.matches.map((m:{match:unknown})=>m.match)).toEqual(source.matches);
  expect(receipt.evaluation.matches[0].exceptions).toHaveLength(0);
  await action(page,"/enable","启用所选例外");
  await page.getByLabel("评估记录 ID",{exact:true}).fill("browser-enabled");
  receipt=await action(page,"/evaluate","按当前修订显式评估");
  const enabled=receipt.evaluation;
  expect(enabled.source).toEqual(source);
  expect(enabled.matches[0].exceptions.map((x:{exception_id:string})=>x.exception_id)).toEqual(["browser-exception"]);
  await safeResultScreenshot(page,"SIMULATED-exceptions-annotated-source.png");
  await action(page,"/disable","停用所选例外");
  await page.getByLabel("评估记录 ID",{exact:true}).fill("browser-disabled-again");
  receipt=await action(page,"/evaluate","按当前修订显式评估");
  expect(receipt.evaluation.source).toEqual(source);
  expect(receipt.evaluation.matches[0].exceptions).toHaveLength(0);
  await action(page,"/archive","归档所选例外");
  const declaration={id:"browser-import",global:true,sourceTaskId:"",ruleId:"",reason:"SIMULATED imported disabled",notBefore:"2020-01-01T00:00:00Z",expiresAt:"2099-01-01T00:00:00Z",conditions:[{field:"event.outcome",operator:"eq",values:["failure"]}]};
  await page.getByLabel("选择声明 JSON 文件",{exact:true}).setInputFiles({name:"SIMULATED-drafts.json",mimeType:"application/json",buffer:Buffer.from(JSON.stringify([declaration]))});
  await expect(page.getByLabel("声明 JSON 数组",{exact:true})).toHaveValue(JSON.stringify([declaration]));
  await action(page,"/import","原子导入（全部禁用）");
  const downloadPromise=page.waitForEvent("download");
  await page.getByRole("button",{name:"下载配置证据 JSON",exact:true}).click();
  const download=await downloadPromise,file=await download.path();expect(file).not.toBeNull();
  const exported=JSON.parse(await readFile(file!,"utf8"));
  expect(exported).toEqual(await(await page.request.get(api+"/export?domainId="+domainId)).json());
  expect(exported.snapshot.entries.find((x:{id:string})=>x.id==="browser-import").enabled).toBe(false);
  await page.reload(); await open(page);
  await page.getByLabel("评估记录 ID",{exact:true}).fill("browser-enabled");
  await page.getByRole("button",{name:"读取已保存评估",exact:true}).click();
  await expect(page.getByLabel("评估结果",{exact:true})).toContainText("browser-exception");
  expect(await(await page.request.get(api+"/evaluation?domainId="+domainId+"&id=browser-enabled")).json()).toEqual(enabled);
  expect((await(await page.request.get(taskURL)).json()).task.result).toEqual(source);
  await safeResultScreenshot(page,"SIMULATED-exceptions-history-preserved.png");
});
