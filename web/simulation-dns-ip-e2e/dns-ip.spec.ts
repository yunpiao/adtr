import { expect, test } from "@playwright/test";
import { mkdir, readFile, writeFile, mkdtemp, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";

test.use({trace:"off",screenshot:"off",video:"off",launchOptions:{env:Object.fromEntries(Object.entries(process.env).filter((e):e is [string,string]=>e[1]!==undefined&&!/^(ADTR_|POSTGRES_|PG)/i.test(e[0])))}});
const api="/api/simulation/dns-ip";
const evidence=resolve("../artifacts/simulation-dns-ip");
type Row={id:string;domain:string;dnsName:string;ip:string;observation:string;source:string;version:string;simulation:boolean};
function expectedRows():Row[]{
 const row=(id:string,name:string,ip:string):Row=>({id,domain:"simulation.example.test",dnsName:name+".simulation.example.test",ip,observation:"synthetic-observation",source:"SIMULATED-fixture",version:"dnsip-sim-v1",simulation:true});
 const rows=Array.from({length:35},(_,i)=>row(`row-${String(i+1).padStart(3,"0")}`,`host-${String(i+1).padStart(3,"0")}`,`192.0.2.${i+1}`));
 rows.splice(1,0,row("multi","host-001","192.0.2.200"));rows.push(row("mapped","mapped","192.0.2.99"),row("v6","v6","2001:db8::1"));return rows;
}
test("fixed synthetic observation exports every matching row and recovers the same actual task",async({page})=>{
 await mkdir(evidence,{recursive:true});
 const temp=await mkdtemp(join(tmpdir(),"adtr-dns-ip-download-"));
 try{
 await page.goto("/");
 await page.getByLabel("用户名",{exact:true}).fill(process.env.ADTR_E2E_USERNAME!);
 await page.getByLabel("密码",{exact:true}).fill(process.env.ADTR_E2E_PASSWORD!);
 await page.getByRole("button",{name:"登录",exact:true}).click();
 await expect(page.getByRole("heading",{name:"登录账户",exact:true})).toBeHidden();
 await page.goto("/#simulation-dns-ip");
 await page.getByRole("radio",{name:"选择数据源 simulation.example.test",exact:true}).check();
 await page.getByRole("button",{name:"核对并打开连接详情",exact:true}).click();
 await expect(page.getByRole("region",{name:"模拟 DNS/IP 工作区",exact:true})).toBeVisible();
 await page.getByLabel("固定观测",{exact:true}).selectOption("synthetic-observation");
 await expect(page.getByTestId("dns-ip-page-summary")).toContainText("38");
 const domain="domainId=simulation-domain-a";
 const rows:Row[]=[];
 for(const offset of [0,20]){
  const r=await page.request.get(`${api}/records?${domain}&observationId=synthetic-observation&keyword=&family=&offset=${offset}&limit=20`);
  expect(r.status()).toBe(200);const v=await r.json();expect(v.total).toBe(38);rows.push(...v.rows);
 }
 expect(rows).toEqual(expectedRows());
 await page.getByRole("button",{name:"下一页",exact:true}).click();
 await expect(page.getByTestId("dns-ip-page-summary")).toContainText("38");
 // Intercept only delivery, never fabricate a server body or successful task.
 let posts=0;let submitted:any;let body:any;
 await page.route(`**${api}/export`,async route=>{posts++;body=route.request().postDataJSON();const response=await route.fetch();expect(response.status()).toBe(200);submitted=await response.json();await route.abort("failed");});
 await page.getByRole("button",{name:"导出全部 38 条匹配记录 XLSX",exact:true}).click();
 await expect.poll(()=>posts).toBe(1);
 await expect.poll(()=>submitted!==undefined).toBe(true);
 await page.unroute(`**${api}/export`);
 await page.getByLabel("恢复操作 ID",{exact:true}).fill(body.operationId);
 await page.getByRole("button",{name:"恢复原操作回执",exact:true}).click();
 const taskID=submitted.submission.task.taskUUID;
 expect(submitted.intent.export).toEqual({observationId:body.observationId,observationHash:body.observationHash,keyword:"",family:"",operationId:body.operationId});
 let manifest:any;
 await expect.poll(async()=>{
  const r=await page.request.get(`${api}/task?${domain}&taskId=${taskID}`);expect(r.status()).toBe(200);
  const v=await r.json();if(v.task.state==="succeeded")manifest=v.task.result;return v.task.state;
 },{timeout:30_000}).toBe("succeeded");
 await page.getByRole("button",{name:"刷新导出任务",exact:true}).click();
 await expect(page.getByRole("button",{name:"下载已验证 XLSX",exact:true})).toBeEnabled();
 const pending=page.waitForEvent("download");
 await page.getByRole("button",{name:"下载已验证 XLSX",exact:true}).click();
 const dl=await pending;const file=join(temp,"actual.xlsx");await dl.saveAs(file);
 const bytes=await readFile(file);expect(createHash("sha256").update(bytes).digest("hex")).toBe(manifest.sha256);
 expect(bytes.length).toBe(manifest.bytes);expect(manifest.rowCount).toBe(38);
 const oracle=join(temp,"expected.json");await writeFile(oracle,JSON.stringify(expectedRows()));
 const validation=JSON.parse(execFileSync("python3",["../scripts/test_simulation_dns_ip_integration.py","--validate-workbook",file,oracle],{encoding:"utf8"}));
 expect(validation.rows).toBe(38);expect(validation.sha256).toBe(manifest.sha256);
 // Explicit metadata/real download rather than claiming the current page is the export.
 await page.reload();
 await page.getByRole("radio",{name:"选择数据源 simulation.example.test",exact:true}).check();
 await page.getByRole("button",{name:"核对并打开连接详情",exact:true}).click();
 await page.getByLabel("恢复操作 ID",{exact:true}).fill(body.operationId);
 await page.getByRole("button",{name:"恢复原操作回执",exact:true}).click();
 await page.getByRole("button",{name:"刷新导出任务",exact:true}).click();
 const again=page.waitForEvent("download");await page.getByRole("button",{name:"下载已验证 XLSX",exact:true}).click();
 const file2=join(temp,"again.xlsx");await(await again).saveAs(file2);expect(await readFile(file2)).toEqual(bytes);
 const result=page.getByRole("region",{name:"SIMULATED DNS/IP 导出结果",exact:true});
 await result.scrollIntoViewIfNeeded();
 const box=await result.boundingBox();const top=await page.locator(".soc-topbar").boundingBox();const viewport=page.viewportSize();
 expect(box).not.toBeNull();expect(viewport).not.toBeNull();expect(box!.y).toBeGreaterThanOrEqual((top?.y??0)+(top?.height??0));expect(box!.y+box!.height).toBeLessThanOrEqual(viewport!.height);
 await result.screenshot({path:resolve(evidence,"SIMULATED-dns-ip-complete-export.png")});
 await page.getByLabel("DNS/IP 字面关键词",{exact:true}).fill("no-such-host");
 await page.getByRole("button",{name:"应用筛选",exact:true}).click();
 await expect(page.getByRole("button",{name:"导出全部 0 条匹配记录 XLSX",exact:true})).toBeVisible();
 }finally{await rm(temp,{recursive:true,force:true});}
});
