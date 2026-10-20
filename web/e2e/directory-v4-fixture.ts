import { expect, type BrowserContext, type Locator, type Page } from "@playwright/test";
import { createHmac, randomUUID } from "node:crypto";
import { isIP } from "node:net";
import { navigateTo } from "./navigation";
import { boundedV4, v4Phase } from "./directory-v4-diagnostics";
import { observeDirectoryV2Responses } from "./directory-v2-response-observer";

// Source-only candidate. All setup writes go through the real UI except the
// existing v4 credential-use grant, whose management UI does not yet exist.
// No task injection, proof bypass, rate-bucket reset, or synthetic server reply.
export type Authenticator = { secret: string; lastCounter: number };
export type Actor = { username: string; password: string; auth: Authenticator; actorId: number };
export type Reader = { username: string; initialPassword: string; password: string; actorId: number; roleId: string };
const button = (page: Page, name: string) => page.getByRole("button", { name, exact: true });
const label = (page: Page, name: string) => page.getByLabel(name, { exact: true });
function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error("Missing isolated v4 fixture input");
  return value;
}
export function requireFixture(suite: "controls" | "readers" | "cancel") {
  if (required("ADTR_E2E_V4_CONTROLS_SUITE") !== `directory-v4-${suite}` ||
      required("ADTR_E2E_LDAP_DIRECTORY_MODE") !== "true" ||
      required("ADTR_E2E_LDAP_DIRECTORY_V4") !== "true" ||
      process.env.ADTR_E2E_LDAP_DIRECTORY_EMPTY === "true" ||
      (process.env.ADTR_E2E_LDAP_DIRECTORY_SLOW === "true") !== (suite === "cancel"))
    throw new Error("Exact owned nonempty v4 fixture is required");
  const base = new URL(required("ADTR_E2E_BASE_URL"));
  if (base.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(base.hostname) || base.username || base.password || base.search || base.hash || base.pathname !== "/")
    throw new Error("Owned loopback API is required");
  const ip = required("ADTR_E2E_LDAP_IP");
  const [a,b] = ip.split(".").map(Number);
  if (isIP(ip) !== 4 || !(a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)) || required("ADTR_E2E_LDAP_USERNAME") !== "fixture@synthetic.invalid" || !/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(required("ADTR_E2E_DB_CONTAINER")))
    throw new Error("Owned synthetic TLS LDAP and database are required");
  for (const key of ["USERNAME", "PASSWORD", "LDAP_PASSWORD", "V4_CONTROLS_PASSWORD", "V4_CONTROLS_PRODUCER_INITIAL_PASSWORD", "V4_CONTROLS_PRODUCER_PASSWORD", "V4_CONTROLS_DRAFT_PASSWORD", "V4_CONTROLS_DRAFT_CODE"])
    required(`ADTR_E2E_${key}`);
  if (suite === "readers") for (const key of ["INITIAL_PASSWORD", "PASSWORD"]) required(`ADTR_E2E_V4_CONTROLS_READER_${key}`);
}
export function totp(secret: string, now = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const char of secret.replace(/=+$/u, "").toUpperCase()) {
    const index = alphabet.indexOf(char);
    if (index < 0) throw new Error("Invalid synthetic TOTP secret");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8) bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac("sha1", Buffer.from(bytes)).update(counter).digest();
  return ((digest.readUInt32BE(digest[digest.length - 1] & 15) & 0x7fffffff) % 1_000_000).toString().padStart(6, "0");
}
export async function freshCode(auth: Authenticator) {
  const earliest = (auth.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest) await new Promise(resolve => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now(); auth.lastCounter = Math.floor(now / 30_000); return totp(auth.secret, now);
}
export async function lateFreshCode(auth: Authenticator) {
  for (;;) {
    const now = Date.now(), counter = Math.floor(now / 30_000);
    if (counter > auth.lastCounter && now % 30_000 >= 26_000 && now % 30_000 < 27_000) {
      auth.lastCounter = counter; return totp(auth.secret, now);
    }
    const target = Math.max(auth.lastCounter + 1, counter + (now % 30_000 >= 27_000 ? 1 : 0));
    await new Promise(resolve => setTimeout(resolve, Math.max(1, target * 30_000 + 26_000 - now)));
  }
}
export async function fillSecret(field: Locator, value: string) {
  try { await field.fill(value); } catch { throw new Error("Unable to enter synthetic authentication proof"); }
}
export async function login(page: Page, username: string, password: string) {
  await expect(page.getByRole("heading", { name: "登录账户", exact: true })).toBeVisible();
  await label(page,"用户名").fill(username); await fillSecret(label(page,"密码"),password); await button(page,"登录").click();
}
export async function changePassword(page: Page, initial: string, next: string) {
  await expect(page.getByRole("heading", { name: "修改密码", exact: true })).toBeVisible();
  await fillSecret(label(page,"当前密码"),initial); await fillSecret(label(page,"新密码"),next); await fillSecret(label(page,"确认新密码"),next);
  await button(page,"更新密码").click(); await expect(page.getByRole("heading",{name:"账户概览",exact:true})).toBeVisible();
}
export async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await navigateTo(page,"多因素认证"); await fillSecret(label(page,"当前密码"),password); await button(page,"开始设置").click();
  const secret = await label(page,"设置密钥").inputValue();
  if (secret.length <= 10) throw new Error("Synthetic MFA enrollment failed");
  const now = Date.now(); await fillSecret(label(page,"认证器验证码"),totp(secret,now)); await button(page,"验证并启用").click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用"); return {secret,lastCounter:Math.floor(now/30_000)};
}
export async function proof(page: Page, password: string, auth: Authenticator) {
  await fillSecret(label(page,"操作者当前密码"),password); await fillSecret(label(page,"未使用的认证器验证码"),await freshCode(auth));
}
export async function readJSON(context: BrowserContext, path: string, actorId?: number) {
  const response = await context.request.get(path,{maxRedirects:0,timeout:15000});
  if (response.status() !== 200 || response.headers()["cache-control"] !== "no-store" || (actorId !== undefined && response.headers()["x-adtr-user-id"] !== String(actorId))) throw new Error("Real fixture read failed identity or status checks");
  return boundedV4(response.json(), 15_000, "Fixture JSON body deadline exceeded");
}
export async function submit(page: Page, path: string, name: string, status = 200) {
  const [response] = await Promise.all([page.waitForResponse(r => new URL(r.url()).pathname === path && r.request().method() === "POST"), button(page,name).click()]);
  if (response.status() !== status || response.headers()["cache-control"] !== "no-store") throw new Error("Real fixture UI mutation failed");
  // Setup endpoints use ordinary JSON; do not apply this helper to native-stream
  // v4 controls, whose response observer belongs to the scenario helper.
  const value = await boundedV4(response.json(), 15_000, "Fixture UI JSON body deadline exceeded");
  return {value};
}
export function safeAssertion(secrets: string[]) {
  return (value: unknown) => {
    const text = JSON.stringify(value);
    if (secrets.some(secret => secret && text.includes(secret)) || /"(?:actorPassword|totpCode|ciphertext|userInfo)"\s*:/u.test(text)) throw new Error("Secret-bearing value rejected from public evidence");
  };
}
async function relogin(page: Page, actor: Actor) {
  await login(page,actor.username,actor.password); await fillSecret(label(page,"二次认证验证码"),await freshCode(actor.auth)); await button(page,"登录").click();
  await expect(page.getByRole("heading",{name:"账户概览",exact:true})).toBeVisible();
}
async function createUser(page: Page, creator: Actor, username: string, initial: string, role: string) {
  await navigateTo(page,"访问管理"); await button(page,"用户管理").click(); await button(page,"新增用户").click();
  await label(page,"用户名").fill(username); await fillSecret(label(page,"初始密码"),initial); await fillSecret(label(page,"确认初始密码"),initial); await label(page,"角色").fill(role);
  await proof(page,creator.password,creator.auth); const {value} = await submit(page,"/api/access/users/create","创建用户");
  if (value.result !== "SUCCESS" || value.sessionRevoked !== false || !Number.isSafeInteger(value.ID) || value.ID <= 0 || value.ID === creator.actorId) throw new Error("Real fixture user creation failed");
  return value.ID as number;
}
async function bindAccount(page: Page, actor: Actor, domain: string, accountLabel: string, safe: (value: unknown) => void) {
  v4Phase("binding-start");
  await navigateTo(page,"管理操作账户"); await button(page,"新增操作账户").click(); await button(page,`选择 ${domain}`).click(); await label(page,"登记标签（可选）").fill(accountLabel);
  await fillSecret(label(page,"AD 用户名"),required("ADTR_E2E_LDAP_USERNAME")); await fillSecret(label(page,"AD 密码"),required("ADTR_E2E_LDAP_PASSWORD")); await proof(page,actor.password,actor.auth);
  const {value} = await submit(page,"/api/operation-accounts/create","保存本地登记"); safe(value); const accountId = value.accountId as string;
  await button(page,"查看凭据使用授权").click(); await label(page,"授权目标角色").selectOption("platform_admin"); await button(page,"审阅角色授权").click();
  await page.getByRole("checkbox",{name:"我已核对目标角色，确认授权覆盖当前及未来全部成员",exact:true}).check(); await proof(page,actor.password,actor.auth);
  const grant = (await submit(page,"/api/credential-use/grant","确认授予角色使用权")).value; safe(grant); expect(grant).toMatchObject({result:"SUCCESS",purpose:"domain.connection_test",allowed:true});
  await navigateTo(page,"域连接"); if (await button(page,"返回域连接列表").isVisible()) await button(page,"返回域连接列表").click();
  await button(page,`查看 ${domain}`).click(); await button(page,"管理凭据来源").click(); await label(page,"新的凭据来源").selectOption("reference"); await button(page,`选择账户 ${accountLabel}`).click(); await proof(page,actor.password,actor.auth);
  const bound = (await submit(page,"/api/domains/credential-source/reference","保存凭据来源")).value; safe(bound); expect(bound).toMatchObject({result:"SUCCESS",credentialSource:"operation_account",replayed:false});
  v4Phase("binding-complete"); return accountId;
}

export async function setupV4(page: Page, context: BrowserContext, options: {reader: boolean}) {
  // Install before the first application document, including future tabs/reloads.
  await observeDirectoryV2Responses(context, "v4-controls");
  v4Phase("setup-start");
  const domain = "synthetic.invalid", groupName = "E2EV4ControlsScope", accountLabel = "Synthetic v4 controls reader";
  const password = required("ADTR_E2E_V4_CONTROLS_PASSWORD"), initial = required("ADTR_E2E_PASSWORD");
  const producerInitial = required("ADTR_E2E_V4_CONTROLS_PRODUCER_INITIAL_PASSWORD"), producerPassword = required("ADTR_E2E_V4_CONTROLS_PRODUCER_PASSWORD");
  const secrets = [initial,password,producerInitial,producerPassword,required("ADTR_E2E_LDAP_USERNAME"),required("ADTR_E2E_LDAP_PASSWORD"),required("ADTR_E2E_V4_CONTROLS_DRAFT_PASSWORD"),required("ADTR_E2E_V4_CONTROLS_DRAFT_CODE")];
  const safe = safeAssertion(secrets);
  await page.goto("/"); await login(page,required("ADTR_E2E_USERNAME"),initial); await changePassword(page,initial,password);
  const bootstrap: Actor = {username:required("ADTR_E2E_USERNAME"),password,auth:await enrollMfa(page,password),actorId:(await readJSON(context,"/api/auth/me")).ID}; secrets.push(bootstrap.auth.secret);
  v4Phase("bootstrap-ready");
  await navigateTo(page,"域连接"); await button(page,"新增域连接").click(); await label(page,"域 DNS 名称").fill(domain); await label(page,"域控 DNS 名称").fill("dc.synthetic.invalid"); await label(page,"连接 IP（可选）").fill(required("ADTR_E2E_LDAP_IP")); await label(page,"初始凭据来源").selectOption("unconfigured"); await proof(page,password,bootstrap.auth);
  const created = (await submit(page,"/api/domains/create-unconfigured","保存本地连接")).value; safe(created); expect(created).toMatchObject({result:"SUCCESS",revision:"1",requiresResourceAssignment:true}); const domainId = created.domainId as string;
  await navigateTo(page,"资源与租户"); await button(page,"资源组").click(); await button(page,"新增资源组").click(); await label(page,"资源组名称").fill(groupName); await label(page,"域 ID").fill(domainId); await proof(page,password,bootstrap.auth); expect((await submit(page,"/api/resources/groups/create","保存资源组")).value).toMatchObject({result:"SUCCESS"});
  await button(page,`查看 ${groupName}`).click(); await button(page,"调整关联角色").click(); await label(page,"显式关联 platform_admin").check(); await proof(page,password,bootstrap.auth); expect((await submit(page,"/api/resources/groups/assign","保存角色关联")).value).toMatchObject({result:"SUCCESS",sessionRevoked:true}); await relogin(page,bootstrap);
  let reader: Reader | undefined;
  if (options.reader) {
    await navigateTo(page,"访问管理"); await button(page,"角色管理").click(); await button(page,"新增角色").click(); await label(page,"角色名称").fill("E2EV4ControlsReader");
    for (const name of ["域连接：读取","目录资产：读取"]) await page.getByRole("checkbox",{name,exact:true}).check(); await proof(page,password,bootstrap.auth);
    const role = (await submit(page,"/api/access/roles/save","保存角色")).value; expect(role).toMatchObject({result:"SUCCESS",sessionRevoked:false}); const roleId = role.roleID as string;
    if (!/^[A-Za-z0-9_-]{24}$/u.test(roleId)) throw new Error("Invalid real reader role identity");
    const permissions = (await readJSON(context,`/api/access/permissions?roleID=${roleId}`)).permissions as {mark:string;auth:{readable:boolean;writeable:boolean}}[];
    expect(permissions.filter(p => p.auth.readable).map(p => p.mark).sort()).toEqual(["directory_assets","domains"]); expect(permissions.every(p => !p.auth.writeable)).toBe(true);
    await navigateTo(page,"资源与租户"); await button(page,"资源组").click(); await button(page,`查看 ${groupName}`).click(); await button(page,"调整关联角色").click(); await expect(label(page,"显式关联 platform_admin")).toBeChecked(); await label(page,"自定义角色 ID").fill(roleId); await proof(page,password,bootstrap.auth); expect((await submit(page,"/api/resources/groups/assign","保存角色关联")).value).toMatchObject({result:"SUCCESS",sessionRevoked:true}); await relogin(page,bootstrap);
    const readerInitial = required("ADTR_E2E_V4_CONTROLS_READER_INITIAL_PASSWORD"), readerPassword = required("ADTR_E2E_V4_CONTROLS_READER_PASSWORD"); secrets.push(readerInitial,readerPassword);
    reader = {username:"v4-controls-reader",initialPassword:readerInitial,password:readerPassword,roleId,actorId:await createUser(page,bootstrap,"v4-controls-reader",readerInitial,roleId)};
  }
  const producerId = await createUser(page,bootstrap,"v4-controls-producer",producerInitial,"platform_admin");
  // Cancel/controls bootstrap uses all ten sensitive writes. Readers bootstrap
  // uses ten by creating its real role/user; producer instead does binding work.
  let accountId: string | undefined;
  if (!options.reader) accountId = await bindAccount(page,bootstrap,domain,accountLabel,safe);
  v4Phase("producer-login");
  await navigateTo(page,"退出登录"); await login(page,"v4-controls-producer",producerInitial); await changePassword(page,producerInitial,producerPassword);
  const producer: Actor = {username:"v4-controls-producer",password:producerPassword,auth:await enrollMfa(page,producerPassword),actorId:(await readJSON(context,"/api/auth/me")).ID}; secrets.push(producer.auth.secret); expect(producer.actorId).toBe(producerId);
  v4Phase("producer-ready");
  if (options.reader) accountId = await bindAccount(page,producer,domain,accountLabel,safe);
  if (!accountId) throw new Error("Missing real account binding");
  v4Phase("source-read");
  const connection = (await readJSON(context,`/api/domains/detail?domainId=${domainId}`,producer.actorId)).connection;
  v4Phase("account-read");
  const account = (await readJSON(context,`/api/operation-accounts/detail?accountId=${accountId}`,producer.actorId)).account;
  v4Phase("effective-read");
  expect(await readJSON(context,`/api/directory-credential-use/v4/effective?accountId=${accountId}`,producer.actorId)).toMatchObject({purpose:"domain.directory_read.v4",explicitlyGranted:false,eligible:false,consumerEnabled:true});
  v4Phase("identity-read");
  const me = await readJSON(context,"/api/auth/me"); expect(me.ID).toBe(producer.actorId); secrets.push(me.csrfToken);
  // A setup-only management POST. No v4 task is admitted here. Fresh current
  // proof and exact account/grant revisions exercise normal server authorization.
  let response;
  v4Phase("grant-proof");
  const grantCode = await freshCode(producer.auth);
  v4Phase("grant-request");
  try {
    response = await context.request.post("/api/directory-credential-use/v4/grant",{headers:{Origin:new URL(page.url()).origin,"X-CSRF-Token":me.csrfToken},data:{accountId,roleId:"platform_admin",purpose:"domain.directory_read.v4",expectedAccountRevision:account.revision,expectedCredentialRevision:account.credentialRevision,expectedGrantRevision:"0",idempotencyKey:randomUUID(),actorPassword:producer.password,totpCode:grantCode},maxRedirects:0,timeout:15000});
  } catch { throw new Error("Real v4 fixture management grant failed"); }
  if (response.status() !== 200 || response.headers()["cache-control"] !== "no-store" || response.headers()["x-adtr-user-id"] !== String(producer.actorId)) throw new Error("Real v4 management grant rejected");
  v4Phase("grant-body");
  const granted = await boundedV4(response.json(), 15_000, "Fixture grant JSON body deadline exceeded"); safe(granted); expect(granted).toMatchObject({purpose:"domain.directory_read.v4",allowed:true,replayed:false,consumerEnabled:true});
  v4Phase("setup-complete");
  return {producer,reader,domainId,accountId,connection,safe,secrets};
}
