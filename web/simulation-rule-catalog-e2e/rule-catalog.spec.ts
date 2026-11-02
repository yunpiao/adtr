import { expect, test, type Page } from "@playwright/test";
import { mkdir, readFile } from "node:fs/promises";
import { resolve } from "node:path";

// Two real logins, no fabricated responses, no session/trace/video artifacts.
test.use({ trace: "off", screenshot: "off", video: "off", launchOptions: {
  env: Object.fromEntries(Object.entries(process.env).filter(
    (e): e is [string,string] => e[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(e[0]),
  )),
} });
const api = "/api/simulation/rule-catalog";
const evidence = resolve("../artifacts/simulation-rule-catalog");
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
async function open(page: Page, route = "simulation-rule-catalog") {
  await page.goto("/#" + route);
  await page.getByRole("radio", {name:"选择数据源 simulation.example.test",exact:true}).check();
  await page.getByRole("button", {name:"核对并打开连接详情",exact:true}).click();
}
async function action(page: Page, route: string, label: string) {
  const response = page.waitForResponse(r => r.url().endsWith(api + route) && r.request().method() === "POST");
  await page.getByRole("button",{name:label,exact:true}).click();
  const done = await response;
  expect(done.status()).toBe(200);
  const receipt=await done.json();
  const input=done.request().postDataJSON();
  const expectedAction=route === "/create-template" ? "copy-template" : route.slice(1);
  expect(receipt.action).toBe(expectedAction);
  expect(receipt.requestHash).toMatch(/^[0-9a-f]{64}$/);
  expect(receipt.intent).toEqual({
    domainId:input.domainId,id:input.id,action:expectedAction,
    operationId:input.operationId,expectedRevision:input.expectedRevision,
    declaration:input.declaration ?? null,cases:input.cases ?? null,
    publishedVersion:input.publishedVersion ?? 0,publishedDigest:input.publishedDigest ?? "",
    templateId:input.templateId ?? "",name:input.name ?? "",acknowledgement:input.acknowledgement ?? "",
  });
  await expect(page.getByLabel("操作回执",{exact:true})).toContainText(receipt.operationId);
  await expect(page.getByRole("button",{name:"browser-rule",exact:true})).toBeEnabled();
  await expect(page.getByRole("button",{name:"下载已发布规则 JSON",exact:true})).toBeVisible();
  return receipt;
}

test("SIMULATED catalog: distinct reviewer, actual cases/download/copy and exact real Worker match", async ({page,browser,context}) => {
  const username=process.env.ADTR_E2E_USERNAME, reviewerName=process.env.ADTR_E2E_REVIEWER, password=process.env.ADTR_E2E_PASSWORD;
  if (!username || !reviewerName || !password) throw new Error("Dedicated disposable simulation harness required");
  await mkdir(evidence,{recursive:true});
  const author = await login(page,username,password);
  await open(page);
  await expect(page.getByRole("region",{name:"模拟规则库入口",exact:true})).toContainText("SIMULATED");
  await page.getByRole("button",{name:"新建独立草稿",exact:true}).click();
  await page.getByLabel("规则 ID",{exact:true}).fill("browser-rule");
  await page.getByLabel("字段 1",{exact:true}).selectOption("event.outcome");
  await page.getByLabel("运算符 1",{exact:true}).selectOption("eq");
  await page.getByLabel("值 1",{exact:true}).fill("failure");
  // Commit once, discard its response, recover original intent without another rule.
  let createPosts=0;
  await page.route(`**${api}/create`,async route => {
    createPosts++;
    const response=await route.fetch(); expect(response.status()).toBe(200);
    await route.abort("failed");
  },{times:1});
  await page.getByRole("button",{name:"保存新草稿",exact:true}).click();
  await expect(page.getByRole("button",{name:"查询回执并安全重试原操作",exact:true})).toBeVisible();
  await page.getByRole("button",{name:"查询回执并安全重试原操作",exact:true}).click();
  await expect(page.getByLabel("操作回执",{exact:true})).toContainText("create");
  expect(createPosts).toBe(1);
  await page.getByRole("button",{name:"browser-rule",exact:true}).click();
  await expect(page.getByLabel("值 1",{exact:true})).toHaveValue("failure");
  await action(page,"/edit","保存草稿修订");
  await page.getByLabel("用例 1 event.outcome",{exact:true}).fill("failure");
  await page.getByLabel("用例 2 event.outcome",{exact:true}).fill("success");
  await page.getByLabel("期望 1",{exact:true}).selectOption("true");
  await page.getByLabel("期望 2",{exact:true}).selectOption("false");
  const tested=await action(page,"/test","执行已保存草稿的正反测试");
  expect(tested.rule.testPassed).toBe(true);
  await expect(page.getByRole("table",{name:"正反用例实际结果",exact:true}).getByRole("row")).toHaveCount(3);
  const actual = JSON.parse(await page.getByLabel("实际测试结果",{exact:true}).innerText());
  expect(actual.caseResults.map((c:{wantMatch:boolean,actualMatch:boolean})=>[c.wantMatch,c.actualMatch])).toEqual([[true,true],[false,false]]);
  await expect(page.getByRole("button",{name:"以当前身份审核并发布模拟版本",exact:true})).toBeDisabled();

  const reviewContext=await browser.newContext({baseURL:process.env.ADTR_E2E_BASE_URL});
  try {
    const reviewPage=await reviewContext.newPage();
    const reviewer=await login(reviewPage,reviewerName,password);
    expect(reviewer.ID).not.toBe(author.ID);
    await open(reviewPage);
    await reviewPage.getByRole("button",{name:"browser-rule",exact:true}).click();
    await expect(reviewPage.getByRole("button",{name:"保存草稿修订",exact:true})).toBeDisabled();
    await action(reviewPage,"/publish","以当前身份审核并发布模拟版本");
    await page.reload(); await open(page);
    await page.getByRole("button",{name:"browser-rule",exact:true}).click();
    await action(page,"/enable","激活最新已发布版本");
    const downloadEvent=page.waitForEvent("download");
    await page.getByRole("button",{name:"下载已发布规则 JSON",exact:true}).click();
    const download=await downloadEvent;
    const file=await download.path(); if (!file) throw new Error("Real downloaded bytes missing");
    const exported=JSON.parse(await readFile(file,"utf8"));
    expect(exported.id).toBe("browser-rule"); expect(exported.simulation).toBe(true);
    const publication=exported.publications[0];
    expect(publication.reviewedBy).toBe(reviewer.ID);
    expect(publication.lastEditorId).toBe(author.ID); expect(publication.testedBy).toBe(author.ID);
    expect(publication.rule.rule.enabled).toBe(false); expect(publication.rule.rule.version).toBe(2);
    await expect(page.getByRole("button",{name:"复制为独立手动模板",exact:true})).toBeDisabled();
    await page.getByLabel("确认独立副本不级联",{exact:true}).check();
    await page.getByLabel("新模板 ID",{exact:true}).fill("browser-copy");
    await page.getByLabel("新模板名称",{exact:true}).fill("SIMULATED catalog browser copy");
    const copied=await action(page,"/create-template","复制为独立手动模板");
    expect(copied.template.version).toBe(1); expect(copied.template.rules[0].version).toBe(1);
    expect(copied.template.rules[0].enabled).toBe(true);
    expect(copied.origin.publishedDigest).toBe(publication.rule.digest);
    expect(copied.origin.templateHash).toBe(copied.template.hash);
    expect(copied.origin.templateHash).not.toBe(copied.origin.publishedDigest);
    await action(page,"/disable","停用活动版本");
    await expect(page.getByRole("region",{name:"模拟规则库工作区",exact:true})).toContainText("不会改变已复制模板");
    await page.getByRole("region",{name:"模拟规则库入口",exact:true}).screenshot({path:resolve(evidence,"SIMULATED-catalog-copy-boundary.png")});
    // Existing manual UI and existing Worker, no new task kind or direct result writes.
    await open(page,"simulation-detection");
    await page.getByRole("button",{name:"查看模板 SIMULATED catalog browser copy",exact:true}).click();
    await page.getByLabel("检测合成数据集",{exact:true}).selectOption("simulation-dataset");
    const runResponse=page.waitForResponse(r=>r.url().endsWith("/api/simulation/detection/run") && r.request().method()==="POST");
    await page.getByRole("button",{name:"手动运行模拟检测",exact:true}).click();
    const submitted=await runResponse; expect(submitted.status()).toBe(200);
    const body=submitted.request().postDataJSON();
    const taskID=(await submitted.json()).submission.task.taskUUID;
    const taskURL=`/api/simulation/detection/task?domainId=simulation-domain-a&taskUUID=${encodeURIComponent(taskID)}`;
    await expect.poll(async()=> (await (await context.request.get(taskURL)).json()).task.state).toBe("succeeded");
    const completed=await (await context.request.get(taskURL)).json();
    expect(completed.task.result.eventCount).toBe(2);
    expect(completed.task.result.templateHash).toBe(copied.template.hash);
    expect(completed.task.result.matches.map((m:{rule_id:string,event_id:string})=>[m.rule_id,m.event_id])).toEqual([["browser-rule","catalog-positive"]]);
    expect(completed.task.result.matches.filter((m:{event_id:string})=>m.event_id==="catalog-negative")).toHaveLength(0);
    const replay=await context.request.post("/api/simulation/detection/run",{data:body,headers:{Origin:process.env.ADTR_E2E_BASE_URL!,"X-CSRF-Token":author.csrfToken}});
    expect(replay.status()).toBe(200); expect((await replay.json()).submission.task.taskUUID).toBe(taskID);
    await page.getByRole("button",{name:"刷新检测任务",exact:true}).click();
    await expect(page.getByLabel("SIMULATED 检测结果",{exact:true})).toContainText("catalog-positive");
    await page.getByRole("region",{name:"模拟检测模板",exact:true}).screenshot({path:resolve(evidence,"SIMULATED-catalog-worker-positive-negative.png")});
    await page.reload(); await open(page);
    await page.getByRole("button",{name:"browser-rule",exact:true}).click();
    expect(await (await context.request.get(taskURL)).json()).toEqual(completed);
    const retained=await context.request.get(api+"/export?domainId=simulation-domain-a&id=browser-rule");
    expect(await retained.json()).toEqual(exported);
  } finally { await reviewContext.close(); }
});
