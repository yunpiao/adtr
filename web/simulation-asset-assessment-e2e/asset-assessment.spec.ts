import { expect, test, type Locator, type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { execFileSync } from "node:child_process";
test.use({trace:"off",screenshot:"off",video:"off",viewport:{width:1440,height:1100},launchOptions:{env:Object.fromEntries(Object.entries(process.env).filter((e):e is [string,string]=>e[1]!==undefined&&!/^(ADTR_|POSTGRES_|PG)/i.test(e[0])))}});
const api="/api/simulation/asset-assessment",domain="domainId=simulation-domain-a";
const evidence=resolve("../artifacts/simulation-asset-assessment");
function verify(report:unknown,scenario:string){execFileSync("python3",["-c","import json,sys;sys.path.insert(0,'../scripts');from test_simulation_asset_assessment_integration import validate_report;validate_report(json.load(sys.stdin),sys.argv[1])",scenario],{input:JSON.stringify(report),stdio:["pipe","pipe","pipe"]});}
async function enter(page:Page){
 await page.goto("/#simulation-asset-assessment");
 await page.getByRole("radio",{name:"选择数据源 simulation.example.test",exact:true}).check();
 await page.getByRole("button",{name:"核对并打开连接详情",exact:true}).click();
 await expect(page.getByRole("region",{name:"模拟资产分类工作区",exact:true})).toBeVisible();
}
async function capture(page:Page,region:Locator,name:string){
 await region.evaluate(element=>element.scrollIntoView({block:"center"}));
 await region.scrollIntoViewIfNeeded();
 await expect(region).toContainText("SIMULATED");
 const box=await region.boundingBox(),top=await page.locator(".soc-topbar").boundingBox(),viewport=page.viewportSize();
 expect(box).not.toBeNull();expect(viewport).not.toBeNull();expect(box!.y).toBeGreaterThanOrEqual((top?.y??0)+(top?.height??0));expect(box!.y+box!.height).toBeLessThanOrEqual(viewport!.height);
 expect(box!.width).toBeGreaterThan(200);expect(box!.height).toBeGreaterThan(100);
 await region.screenshot({path:resolve(evidence,name)});
}
test("SIMULATED synchronous immutable report: oracle, history, full statistics and actual response-loss recovery",async({page})=>{
 await mkdir(evidence,{recursive:true});await page.goto("/");
 await page.getByLabel("用户名",{exact:true}).fill(process.env.ADTR_E2E_USERNAME!);
 await page.getByLabel("密码",{exact:true}).fill(process.env.ADTR_E2E_PASSWORD!);
 await page.getByRole("button",{name:"登录",exact:true}).click();
 await expect(page.getByRole("heading",{name:"登录账户",exact:true})).toBeHidden();await enter(page);
 await page.getByLabel("固定评估场景",{exact:true}).selectOption("baseline");
 // Fetch the real response, then lose delivery. Never synthesize a success body.
 let posts=0,receipt:any,body:any;
 await page.route(`**${api}/create`,async route=>{posts++;body=route.request().postDataJSON();const response=await route.fetch();expect(response.status()).toBe(200);receipt=await response.json();await route.abort("failed");});
 await page.getByRole("button",{name:"创建不可变模拟报告",exact:true}).click();
 await expect.poll(()=>receipt!==undefined).toBe(true);expect(posts).toBe(1);
 await page.unroute(`**${api}/create`);
 await page.getByRole("button",{name:"查询原操作回执",exact:true}).click();
 const detail=page.getByRole("region",{name:"报告详情",exact:true}),summary=page.getByRole("region",{name:"完整报告统计",exact:true});
 await expect(detail.getByRole("heading",{name:receipt.report.id,exact:true})).toBeVisible();verify(receipt.report,"baseline");
 const originalSummary=await summary.innerText();await expect(summary).toContainText("已观察对象总数：11");
 await expect(detail.locator("tbody tr")).toHaveCount(11);
 await expect(detail.getByRole("row").filter({hasText:"computer-missing"})).toContainText("unknown / missing_evidence");
 await expect(detail.getByRole("row").filter({hasText:"user-disabled-never"})).toContainText("never_observed / explicit_never_assertion");
 await page.getByLabel("对象类别",{exact:true}).selectOption("group");
 await expect(detail.locator("tbody tr")).toHaveCount(4);expect(await summary.innerText()).toBe(originalSummary);
 await expect(detail.getByRole("row").filter({hasText:"group-unknown"})).toContainText("not_applicable / asset_kind");
 // APIRequestContext does NOT add browser Origin automatically: set it and CSRF explicitly.
 const profileResponse=await page.request.get("/api/auth/me");expect(profileResponse.status()).toBe(200);const profile=await profileResponse.json();
 const replay=await page.request.post(`${api}/create`,{headers:{Origin:process.env.ADTR_E2E_BASE_URL!,"X-CSRF-Token":profile.csrfToken},data:body});
 expect(replay.status()).toBe(200);expect(await replay.json()).toEqual(receipt);
 const historyBefore=await page.request.get(`${api}/history?${domain}&offset=0&limit=20`);expect(historyBefore.status()).toBe(200);const savedHistory=await historyBefore.json();expect(savedHistory.reports.filter((r:any)=>r.id===receipt.report.id)).toHaveLength(1);
 await capture(page,summary,"SIMULATED-asset-assessment-full-statistics.png");
 await page.reload();await enter(page);
 const history=page.getByRole("region",{name:"报告历史",exact:true});
 const historyButton=history.getByRole("button",{name:new RegExp(`^${receipt.report.id} · baseline`)});
 await expect(historyButton).toBeVisible();await historyButton.click();
 await expect(detail.getByRole("heading",{name:receipt.report.id,exact:true})).toBeVisible();expect(await summary.innerText()).toBe(originalSummary);
 await expect(history).toContainText(receipt.report.id);await capture(page,history,"SIMULATED-asset-assessment-populated-history.png");
 await page.getByLabel("operationId",{exact:true}).fill(body.operationId);await page.getByRole("button",{name:"查询原操作回执",exact:true}).click();
 await expect(detail).toContainText(receipt.report.hash);
 const loaded=await page.request.get(`${api}/detail?${domain}&id=${receipt.report.id}`);expect(loaded.status()).toBe(200);expect(await loaded.json()).toEqual(receipt.report);
 for(const scenario of ["freshness-boundary","stale"]){
  await page.getByLabel("固定评估场景",{exact:true}).selectOption(scenario);
  const response=page.waitForResponse(r=>r.url().endsWith(`${api}/create`)&&r.request().method()==="POST");
  await page.getByRole("button",{name:"创建不可变模拟报告",exact:true}).click();const created=await response;expect(created.status()).toBe(200);const actual=await created.json();verify(actual.report,scenario);
  await expect(detail.getByRole("heading",{name:actual.report.id,exact:true})).toBeVisible();
  if(scenario==="stale")await expect(detail.getByRole("row").filter({hasText:"user-enabled-recent"})).toContainText("unknown / stale_observation");
 }
});
