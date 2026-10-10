/* Local administration interactions. No credential capture or external execution. */
window.ADTR_ADMIN_ENGINE = function (ctx) {
  'use strict';
  const { api, pages, memory, visible, get, record, edit, detail, confirm, render } = ctx;
  const { state, escape: esc, tag, btn, icon, openDialog, closeDialog, toast } = api;
  const $ = id => document.getElementById(id);
  const now = () => new Date().toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
  const all = id => memory[id] || [];
  const page = name => pages['admin/' + name];
  const busy = new Map();
  let serial = 1000;
  let proof = null;
  let activeAction = null;
  const dateNow = () => new Date('2026-10-10T06:35:00Z');
  const pretty = value => value == null || value === '' ? '未设置' : typeof value === 'boolean' ? (value ? '已启用' : '未启用') : Array.isArray(value) ? value.join('、') : String(value);
  const line = (label, value) => `<div class="info-row"><span>${esc(label)}</span><strong>${esc(pretty(value))}</strong></div>`;
  const errorBox = () => '<div id="admin-error" class="error-inline" role="alert"></div>';
  const fail = message => { const box = $('admin-error') || $('biz-error'); if (box) box.textContent = message; else toast(message); };
  const button = (p, r, a, style = '') => btn(esc(a.label), 'biz-action', style, `data-page="${esc(p.id)}" data-id="${esc(r?.id || '')}" data-verb="${esc(a.verb)}" data-intent="${esc(a.intent || a.verb)}"`);
  const avatarClass = name => ({ '青绿': 'avatar-green', '靛蓝': 'avatar-blue', '暖橙': 'avatar-orange', '石墨': 'avatar-slate' })[name] || 'avatar-green';
  const fieldsFor = (p, r) => (p.fields || []).filter(f => !r?.fieldKeys || r.fieldKeys.includes(f.key));
  const username = () => ({ operator: 'demo.operator', viewer: 'demo.viewer', platform: 'demo.platform', maintainer: 'demo.maintainer', newuser: 'demo.new', expireduser: 'demo.expired' })[state.role] || '';
  function isSelf(r) { return r.username === username() || r.name === username(); }
  function canChange(p) { return !!ctx.writable(p); }
  function snapshot(p, rows = [], writing = true, loginOnly = false) {
    return { p, rows: rows.map(r => ({ id: r.id, version: r.version })), epoch: state.epoch, role: state.role, domain: state.domain, writing, loginOnly, loginAccount: loginOnly ? rows[0]?.name : null, permissionAction: activeAction?.page === p.id ? structuredClone(activeAction.action) : null, used: false };
  }
  function loginCandidate(r) {
    const owner = all('admin/users').find(u => u.username === username());
    const ownerAllowed = owner ? !owner.archived && owner.status === '启用' : ['demo.new', 'demo.expired'].includes(r?.name);
    return !!r && r.name === username() && ownerAllowed && state.adminAccessOverrides?.[state.role]?.enabled !== false;
  }
  function valid(s, consume = false) {
    if (s.loginOnly) {
      const target = s.rows.length === 1 ? all('admin/accounts').find(r => r.id === s.rows[0].id) : null;
      if (s.used || !state.signedOut || s.p.id !== 'admin/accounts' || s.epoch !== state.epoch || s.role !== state.role || s.domain !== state.domain || s.loginAccount !== username() || !loginCandidate(target) || target.version !== s.rows[0].version) {
        closeDialog(); toast('登录状态或账号已变化，请重新开始。'); return false;
      }
      if (consume) s.used = true;
      return true;
    }
    if (s.used || s.epoch !== state.epoch || s.role !== state.role || s.domain !== state.domain || !ctx.allowed(s.p) || (s.permissionAction && !ctx.canAction(s.p, s.permissionAction)) || (s.writing && !canChange(s.p)) || s.rows.some(t => !get(s.p, t.id) || get(s.p, t.id).version !== t.version)) {
      closeDialog(); toast('对象或访问权限已变化，请重新打开。'); return false;
    }
    if (consume) s.used = true;
    return true;
  }
  function appendAudit(text, target) {
    const list = all('admin/audit');
    list.unshift({ id: 'audit-local-' + (++serial), domain: 'platform', name: text, actor: username(), auditType: /角色|用户|认证|密码|身份|账号|资源/.test(text) ? '身份与权限' : /服务|运维|部署/.test(text) ? '服务运维' : /授权应用|第三方/.test(text) ? '集成授权' : '系统设置', target, result: /未通过|失败/.test(text) ? '失败' : '成功', time: now(), status: '可见', archived: false, requestId: 'req-local-' + serial, changes: text, origin: '管理控制台', visibility: 'platform', version: 1 });
  }
  function commit(p, r, text) { record(p, r, text); r.updated = now(); if (r.history?.length) r.history[r.history.length - 1].time = now(); appendAudit(text, r.name); }
  function complete(p, r, text) { commit(p, r, text); closeDialog(); render(); }
  function frozenApply(p, rows, a, fn) {
    confirm(p, rows, a, reason => { fn(reason); });
  }
  function verified() { return proof && proof.epoch === state.epoch && proof.role === state.role && proof.domain === state.domain && Date.now() - proof.at < 300000; }
  function requireProof(p, rows, next) {
    if (verified()) { next(); return; }
    const s = snapshot(p, rows);
    openDialog('验证操作身份', `<div class="mfa-steps"><div class="security-shield">${icon('lock')}</div><div><h3>${esc(username())}</h3><p>此操作需要近期身份验证。验证完成后可继续核对变更。</p></div></div>${line('验证用途', p.title)}<label class="check-label"><input id="admin-proof-check" type="checkbox">确认以当前账号继续</label>${errorBox()}`, btn('完成验证', 'confirm-intent', 'primary'), () => {
      if (!valid(s)) return;
      if (!$('admin-proof-check')?.checked) { fail('请确认当前账号。'); return; }
      proof = { epoch: state.epoch, role: state.role, domain: state.domain, at: Date.now() };
      const mfa = all('admin/mfa').find(r => r.name === username());
      if (mfa) { mfa.verified = true; mfa.lastVerified = now(); }
      s.used = true; closeDialog(); next();
    });
  }
  function fieldMarkup(f, values) {
    const value = values[f.key] ?? (f.type === 'checkbox' ? false : f.options?.[0] ?? '');
    const id = 'biz-field-' + f.key;
    const input = f.type === 'select' ? `<select id="${esc(id)}">${(f.options || []).map(o => `<option value="${esc(o)}" ${o === value ? 'selected' : ''}>${esc(o)}</option>`).join('')}</select>` : f.type === 'checkbox' ? `<input type="checkbox" id="${esc(id)}" ${value ? 'checked' : ''}>` : f.type === 'textarea' ? `<textarea id="${esc(id)}" rows="3" maxlength="800">${esc(value)}</textarea>` : `<input id="${esc(id)}" type="${f.type === 'number' ? 'number' : 'text'}" value="${esc(value)}" maxlength="200" ${f.min != null ? `min="${f.min}"` : ''} ${f.max != null ? `max="${f.max}"` : ''}>`;
    return `<label class="form-field"><span>${esc(f.label)}${f.required ? ' *' : ''}</span>${input}</label>`;
  }
  function readFields(fields) {
    const values = {};
    for (const f of fields) {
      const input = $('biz-field-' + f.key);
      let value = f.type === 'checkbox' ? !!input?.checked : input?.value.trim() ?? '';
      if (f.required && value === '') { fail('请填写' + f.label + '。'); input?.focus(); return null; }
      if (f.type === 'number') {
        value = Number(value);
        if (!Number.isFinite(value) || !Number.isInteger(value) || (f.min != null && value < f.min) || (f.max != null && value > f.max)) { fail(f.label + '不在允许范围内。'); input?.focus(); return null; }
      }
      if (f.type === 'select' && !f.options.includes(value)) { fail('请选择有效的' + f.label + '。'); return null; }
      values[f.key] = value;
    }
    return values;
  }
  function form(p, r, title, fields, values, save, intro = '') {
    const s = snapshot(p, r ? [r] : []);
    openDialog(title, `${intro}<div class="form-grid">${fields.map(f => fieldMarkup(f, values)).join('')}</div>${errorBox()}`, btn('保存', 'confirm-intent', 'primary'), () => {
      if (!valid(s)) return;
      const result = readFields(fields); if (!result) return;
      if (save(result) === false) return;
      s.used = true;
    });
  }
  function inspect(title, pairs, extra = '', footer = '') { openDialog(title, pairs.map(([k, v]) => line(k, v)).join('') + extra, footer); }
  function newRow(p, values) { const r = { id: 'admin-local-' + (++serial), domain: 'platform', status: '启用', version: 0, ...values }; memory[p.id].unshift(r); return r; }
  function downloaded(p, records, filename, text) {
    if (!canChange(p) || state.downloadRevoked || !records.length || records.some(r => !get(p, r.id))) { fail('当前下载权限已失效，或记录不可用。'); return; }
    const data = 'ADTR · 演示数据\n\n' + text;
    const url = URL.createObjectURL(new Blob([data], { type: 'text/plain;charset=utf-8' }));
    const a = document.createElement('a'); a.href = url; a.download = filename; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    for (const r of records) { r.lastDownloaded = now(); commit(p, r, '下载' + p.title); }
    render();
  }
  function ownRole() { const user = all('admin/users').find(r => r.username === username()); return all('admin/roles').find(r => r.name === user?.role); }
  function updateAccess() {
    const roleRows = all('admin/roles'), userRows = all('admin/users'), groupRows = all('admin/resources');
    const result = {};
    for (const [key, account] of Object.entries({ operator: 'demo.operator', viewer: 'demo.viewer', platform: 'demo.platform', maintainer: 'demo.maintainer' })) {
      const u = userRows.find(x => x.username === account), r = roleRows.find(x => x.name === u?.role);
      if (!u || !r) continue;
      const domains = [...new Set(groupRows.filter(g => !g.archived && (g.roleIds || []).includes(r.id)).flatMap(g => g.domains || []))];
      const permissions = Object.fromEntries((page('roles')?.permissionMatrix || []).flatMap(g => g.items).map(i => [i.key, !!r[i.key]]));
      result[key] = { permissions, domains, enabled: u.status === '启用', roleId: r.id };
    }
    state.adminAccessOverrides = result;
    for (const r of roleRows) {
      r.members = userRows.filter(u => !u.archived && u.role === r.name).map(u => u.id);
      r.memberCount = r.members.length;
      r.resourceGroup = groupRows.filter(g => !g.archived && (g.roleIds || []).includes(r.id)).map(g => g.name).join('、') || '无域授权';
    }
    for (const u of userRows) u.resourceGroup = roleRows.find(r => r.name === u.role)?.resourceGroup || '无域授权';
  }
  function roleMutationBlocked(r, values = {}) {
    if (r && ownRole()?.id === r.id) return '不能修改当前登录身份所属角色的权限。';
    if (r?.lastAdministrator && (values.userManage === false || values.roleManage === false || values.resourceManage === false)) return '必须保留最后一个平台管理员的身份与授权管理权限。';
    const dependencies = [['assetExport', 'assetView'], ['detectionRun', 'detectionView'], ['ruleManage', 'detectionView'], ['responseExecute', 'responseView'], ['reportExport', 'reportView'], ['auditExport', 'auditView'], ['systemManage', 'systemView'], ['serviceOperate', 'systemView']];
    for (const [write, read] of dependencies) if (values[write] && !values[read]) return '操作权限必须同时启用对应的查看权限。';
    return null;
  }
  function editRole(p, r) {
    const blocked = roleMutationBlocked(r); if (blocked) { inspect('权限变更受限', [['角色', r.name], ['原因', blocked]]); return; }
    requireProof(p, r ? [r] : [], () => {
      const s = snapshot(p, r ? [r] : []), values = r || {};
      const fields = p.fields || [];
      const groups = p.permissionMatrix.map(g => `<section class="panel"><div class="panel-head"><h3>${esc(g.title)}</h3></div><div class="form-grid panel-body">${g.items.map(i => fieldMarkup({ key: i.key, label: i.label, type: 'checkbox' }, values)).join('')}</div></section>`).join('');
      openDialog(r ? '编辑角色权限' : '新增角色', `<div class="form-grid">${fields.filter(f => f.type !== 'checkbox').map(f => fieldMarkup(f, values)).join('')}</div><div class="settings-sections">${groups}</div><label class="check-label"><input id="admin-role-impact" type="checkbox">已核对权限与受影响成员，保存后要求重新登录</label>${errorBox()}`, btn('保存权限', 'confirm-intent', 'primary'), () => {
        if (!valid(s)) return;
        const vals = readFields(fields); if (!vals) return;
        const problem = roleMutationBlocked(r, vals); if (problem) { fail(problem); return; }
        if (!$('admin-role-impact')?.checked) { fail('请确认受影响成员与会话。'); return; }
        if (all(p.id).some(x => x.id !== r?.id && x.name === vals.name && !x.archived)) { fail('角色名称已存在。'); return; }
        const oldName = r?.name, target = r || newRow(p, { members: [], memberCount: 0, resourceGroup: '无域授权' });
        Object.assign(target, vals); target.permissionSummary = p.permissionMatrix.flatMap(g => g.items).filter(i => target[i.key]).map(i => i.label).slice(0, 3).join('、') || '尚未授予权限';
        all('admin/users').filter(u => u.role === oldName).forEach(u => { u.role = target.name; u.sessions = '已撤销'; });
        target.status = '启用'; updateAccess(); s.used = true; complete(p, target, '保存角色权限并撤销受影响会话');
      });
    });
  }
  function editUsers(p, r, roleOnly, rows) {
    const targets = rows?.length ? rows : r ? [r] : [];
    if (targets.some(isSelf) && roleOnly) { inspect('角色分配受限', [['原因', '不能调整当前登录身份自己的角色。']]); return; }
    requireProof(p, targets, () => {
      let fields = roleOnly ? p.fields.filter(f => f.key === 'role') : p.fields.filter(f => f.key !== 'resourceGroup');
      if (r && isSelf(r)) fields = fields.filter(f => !['role', 'status', 'resourceGroup', 'username'].includes(f.key));
      if (r?.lastAdministrator) fields = fields.filter(f => !['role', 'status', 'resourceGroup', 'username'].includes(f.key));
      const s = snapshot(p, targets);
      openDialog(roleOnly ? (targets.length > 1 ? '批量分配角色' : '分配角色') : r ? '编辑用户' : '新增用户', `${targets.length ? `<div class="response-target">${targets.map(t => `<p>${esc(t.name)} · ${esc(t.username)}</p>`).join('')}</div>` : ''}<div class="form-grid">${fields.map(f => fieldMarkup(f, r || {})).join('')}</div>${roleOnly ? '<p>角色变更后，所选用户的活动会话将失效。</p>' : ''}${errorBox()}`, btn('保存用户', 'confirm-intent', 'primary'), () => {
        if (!valid(s)) return;
        const vals = readFields(fields); if (!vals) return;
        if (vals.username && !/^demo\.[a-z0-9._-]+$/i.test(vals.username)) { fail('登录账号需使用 demo. 开头的账户名称。'); return; }
        if (vals.email && !/^[a-z0-9._+-]+@example\.test$/i.test(vals.email)) { fail('联系邮箱需使用 example.test 地址。'); return; }
        if (vals.username && all(p.id).some(u => u.id !== r?.id && u.username === vals.username && !u.archived)) { fail('登录账号已存在。'); return; }
        if (roleOnly && targets.some(t => t.lastAdministrator && vals.role !== '平台管理员')) { fail('不能移除最后一个平台管理员。'); return; }
        if (roleOnly && targets.some(isSelf)) { fail('不能修改当前登录身份自己的角色。'); return; }
        const list = targets.length ? targets : [newRow(p, { mfa: '待绑定', lastLogin: '尚未登录', passwordState: '首次登录需更新' })];
        list.forEach(t => { const changed = vals.role && vals.role !== t.role; Object.assign(t, vals); if (changed) t.sessions = '已撤销'; commit(p, t, roleOnly ? '分配用户角色' : r ? '编辑用户' : '新增用户'); });
        updateAccess(); s.used = true; closeDialog(); render();
      });
    });
  }
  function editResource(p, r, associationOnly) {
    if (r && (r.roleIds || []).includes(ownRole()?.id)) { inspect('资源授权变更受限', [['资源组', r.name], ['原因', '不能调整当前登录身份正在使用的资源授权。']]); return; }
    requireProof(p, r ? [r] : [], () => {
      const fields = associationOnly ? p.fields.filter(f => f.key === 'roles') : p.fields;
      form(p, r, associationOnly ? '关联角色' : r ? '编辑资源组' : '新增资源组', fields, r || {}, values => {
        const roleNames = (values.roles ?? r?.roles ?? '').split('、');
        const ids = all('admin/roles').filter(x => roleNames.includes(x.name)).map(x => x.id);
        if (ids.includes(ownRole()?.id)) { fail('不能为当前登录身份修改资源授权。'); return false; }
        if (values.name && all(p.id).some(x => x.id !== r?.id && !x.archived && x.name === values.name)) { fail('资源组名称已存在。'); return false; }
        const target = r || newRow(p, {}); Object.assign(target, values); target.roleIds = ids;
        target.domains = (target.domainSet || '').includes('alpha.example.test') ? ['alpha'] : [];
        if ((target.domainSet || '').includes('beta.example.test')) target.domains.push('beta');
        target.status = ids.length ? '启用' : '未分配'; updateAccess(); complete(p, target, '保存资源范围与角色关联');
      }, '<p>资源范围与功能权限分别生效。受影响角色的旧会话将失效。</p>');
    });
  }
  function choose(p, key, next) {
    const field = { ...p.fields.find(f => f.key === key), options: visible(p).map(r => r.name) }, s = snapshot(p, []);
    openDialog('选择账号', fieldMarkup(field, {}) + errorBox(), btn('继续', 'confirm-intent', 'primary'), () => {
      if (!valid(s)) return; const values = readFields([field]); if (!values) return;
      const target = visible(p).find(r => r.name === values[key]);
      if (!target || !get(p, target.id)) { fail('此账号不可用。'); return; } s.used = true; next(target);
    });
  }
  function loginFlow(p, r, loginOnly = false) {
    if (loginOnly ? (!state.signedOut || !loginCandidate(r)) : (!ctx.allowed(p) || !get(p, r.id))) { inspect('账号不可用', [['原因', '当前身份不能访问此账号。']]); return; }
    const changeNeeded = r.authFlow !== 'active' || r.passwordState !== '有效';
    const steps = changeNeeded ? ['确认账号', '更新安全信息', '验证身份', '进入工作区'] : ['确认账号', '验证身份', '进入工作区'];
    const s = snapshot(p, [r], true, loginOnly); let step = 0;
    function draw() {
      const label = steps[step];
      const content = label === '确认账号' ? line('账号', r.name) + line('密码状态', r.passwordState) : label === '更新安全信息' ? '<p>安全更新后，其他活动会话将失效。完成此步骤后仍需要身份验证。</p>' : label === '验证身份' ? `<div class="mfa-steps"><div class="security-shield">${icon('lock')}</div><div><h3>确认登录身份</h3><p>${esc(r.name)} · 身份验证器</p></div></div><label class="check-label"><input id="admin-login-proof" type="checkbox">完成本次身份验证</label>` : '<div class="state-banner info">账号检查与身份验证已完成，可以进入工作区。</div>';
      openDialog('账号登录', `<div class="wizard-steps">${steps.map((x, i) => `<span class="${i === step ? 'current' : ''}">${i + 1} ${esc(x)}</span>`).join('')}</div>${content}${errorBox()}`, btn(step === steps.length - 1 ? '进入工作区' : '下一步', 'confirm-intent', 'primary'), () => {
        if (!valid(s)) return;
        if (label === '验证身份' && !$('admin-login-proof')?.checked) { fail('请先完成身份验证。'); return; }
        if (label === '更新安全信息') { r.passwordState = '有效'; r.authFlow = 'active'; r.sessions = '旧会话已失效'; r.status = '待验证'; commit(p, r, '完成首次登录或过期密码更新'); s.rows[0].version = r.version; }
        if (label === '验证身份') { r.loginVerified = true; r.mfa = '已启用'; const m = all('admin/mfa').find(x => x.name === r.name); if (m) { m.enabled = true; m.status = '已启用'; m.verified = true; m.lastVerified = now(); } }
        if (step === steps.length - 1) {
          if (!r.loginVerified) { fail('请重新完成身份验证。'); return; }
          s.used = true; all(p.id).forEach(x => { if (x.id !== r.id && x.status === '已登录') { x.status = '已退出'; x.sessions = '已失效'; } });
          r.status = '已登录'; r.lastLogin = now(); r.sessions = '当前浏览器'; commit(p, r, '完成账号登录'); closeDialog(); api.login(r.name); return;
        }
        step++; draw();
      });
    }
    r.loginVerified = false; draw();
  }
  function resumeSession() {
    if (!state.signedOut) return false;
    const p = page('accounts');
    const account = all('admin/accounts').find(r => r.name === username());
    if (!p || !loginCandidate(account)) {
      inspect('账号暂时无法登录', [['原因', '当前账号不可用，请联系管理员。']]);
      return true;
    }
    proof = null;
    loginFlow(p, account, true);
    return true;
  }
  function passwordChange(p, r, administrative) {
    requireProof(p, [r], () => frozenApply(p, [r], { label: administrative ? '重置指定用户密码' : '更新账号安全信息', verb: 'reset', confirm: true }, reason => {
      const targetAccount = all('admin/accounts').find(a => a.name === (r.username || r.name));
      const stateValue = administrative ? '首次登录需更新' : '有效';
      r.passwordState = stateValue; r.sessions = '已失效'; r.loginVerified = false;
      if (p.id === 'admin/accounts') { r.authFlow = administrative ? 'first_login' : 'active'; r.status = '待验证'; }
      if (targetAccount && targetAccount !== r) { targetAccount.passwordState = stateValue; targetAccount.authFlow = administrative ? 'first_login' : 'active'; targetAccount.status = '待验证'; targetAccount.sessions = '已失效'; targetAccount.loginVerified = false; }
      r.resetReason = reason; commit(p, r, administrative ? '重置密码并要求下次登录更新' : '完成安全更新，等待身份验证');
    }));
  }
  function mfaFlow(p, r) {
    if (!ctx.allowed(p) || !get(p, r.id)) { inspect('账号不可用', [['原因', '当前身份不能访问此账号。']]); return; }
    let step = 0; const s = snapshot(p, [r]);
    const steps = ['关联验证器', '验证身份', '确认保护'];
    function draw() {
      const body = step === 0 ? `<div class="mfa-steps"><div class="security-shield">${icon('lock')}</div><div><h3>身份验证器</h3><p>为 ${esc(r.name)} 添加额外登录保护。</p></div></div>${line('验证要求', r.requirement)}` : step === 1 ? '<div class="state-banner info">验证器已就绪，请完成本次验证。</div><label class="check-label"><input id="admin-mfa-proof" type="checkbox">完成身份验证</label>' : '<p>启用后，登录与敏感操作都将按账号策略要求额外验证。</p><label class="check-label"><input id="admin-mfa-recovery" type="checkbox">已确认绑定账号与恢复方式</label>';
      openDialog('启用多因素认证', `<div class="wizard-steps">${steps.map((x, i) => `<span class="${i === step ? 'current' : ''}">${i + 1} ${esc(x)}</span>`).join('')}</div>${body}${errorBox()}`, btn(step === 2 ? '启用保护' : '下一步', 'confirm-intent', 'primary'), () => {
        if (!valid(s)) return;
        if (step === 1 && !$('admin-mfa-proof')?.checked) { fail('请完成身份验证。'); return; }
        if (step === 2 && !$('admin-mfa-recovery')?.checked) { fail('请确认绑定账号与恢复方式。'); return; }
        if (step < 2) { step++; draw(); return; }
        s.used = true; r.enabled = true; r.verified = true; r.status = '已启用'; r.boundAt = now(); r.lastVerified = now();
        const account = all('admin/accounts').find(x => x.name === r.name); if (account) account.mfa = '已启用';
        complete(p, r, '启用多因素认证');
      });
    }
    draw();
  }
  function licenseCheck(p, r) {
    const errors = [];
    if (!r.deviceMatched || r.deviceId !== p.currentDevice) errors.push('绑定设备与当前设备不匹配');
    const start = Date.parse(r.starts), end = Date.parse(r.expires + 'T23:59:59Z');
    if (!Number.isFinite(start) || !Number.isFinite(end) || start > dateNow().getTime() || end < dateNow().getTime()) errors.push('许可不在有效期内');
    if (Number(r.domainsAllowed) < p.currentUsage.domains || Number(r.objectsAllowed) < p.currentUsage.objects) errors.push('授权额度小于当前用量');
    if (!r.packageReady) errors.push('许可文件尚未就绪');
    return errors;
  }
  function licensePreview(p, r) {
    const errors = licenseCheck(p, r);
    inspect('授权预览', [['许可', r.name], ['设备标识', r.deviceId], ['有效期', r.starts + ' 至 ' + r.expires], ['功能范围', r.modules], ['授权额度', r.quota], ['当前用量', `${p.currentUsage.domains} 个域 / ${p.currentUsage.objects} 个对象`], ['校验结果', errors.length ? errors.join('；') : '设备、有效期与额度匹配']], '', button(p, r, { label: '校验许可', verb: 'test', intent: 'license_validate' }) + button(p, r, { label: '激活许可', verb: 'activate', intent: 'license_activate' }, 'primary'));
  }
  function stateSequence(p, r, steps, finish) {
    const key = p.id + '/' + r.id;
    if (busy.has(key)) { inspect('操作进行中', [['目标', r.name], ['当前状态', r.status], ['恢复进度', r.recovery || r.stage || '处理中']]); return; }
    const s = snapshot(p, [r]); busy.set(key, s); let step = 0;
    function advance() {
      if (s.epoch !== state.epoch || s.role !== state.role || s.domain !== state.domain || !canChange(p) || (s.permissionAction && !ctx.canAction(p, s.permissionAction)) || !get(p, r.id) || get(p, r.id).version !== s.rows[0].version) { busy.delete(key); return; }
      if (step < steps.length) {
        const current = steps[step++]; r.status = current.status; r.recovery = current.label; r.stage = current.label; r.progress = Math.round(step / (steps.length + 1) * 100);
        if (r.steps) r.steps.forEach((item, i) => { item.status = i < step - 1 ? '已完成' : i === step - 1 ? '进行中' : '待执行'; });
        commit(p, r, current.label); s.rows[0].version = r.version; render(); setTimeout(advance, 550); return;
      }
      busy.delete(key); finish(); render();
    }
    advance();
  }
  function operationsReady(p) {
    const s = p.session;
    return s?.status === '已登录' && s.epoch === state.epoch && s.role === state.role && verified() && s.verified;
  }
  function operationsLogin(p) {
    requireProof(p, [], () => {
      const s = snapshot(p, []);
      openDialog('进入运维工作区', `${line('账号', username())}${line('操作范围', '平台服务与运行配置')}<p>服务操作前仍需核对具体目标、影响范围与恢复条件。</p>${errorBox()}`, btn('进入运维', 'confirm-intent', 'primary'), () => {
        if (!valid(s, true)) return;
        p.session = { status: '已登录', account: username(), verified: true, epoch: state.epoch, role: state.role, expires: new Date(Date.now() + 300000).toISOString() };
        api.audit('进入运维工作区', username(), '成功', 'platform'); appendAudit('进入运维工作区', username()); closeDialog(); render();
      });
    });
  }
  function serviceChange(p, r, restart) {
    if (!operationsReady(p)) { inspect('需要运维验证', [['目标', r.name], ['当前状态', p.session?.status || '未登录'], ['下一步', '请先进入运维工作区并完成身份验证。']], '', button(p, null, { label: '进入运维', verb: 'activate', intent: 'operations_login' }, 'primary')); return; }
    if (!restart && r.id === 'service-platform') { inspect('操作不可用', [['目标', r.name], ['原因', '整个平台只支持带恢复检查的重启操作。']]); return; }
    if (busy.has(p.id + '/' + r.id)) { inspect('操作进行中', [['目标', r.name], ['当前阶段', r.recovery]]); return; }
    const willStop = !restart && r.status === '运行中';
    const label = restart ? '重启服务' : willStop ? '停止服务' : '启动服务';
    const s = snapshot(p, [r]);
    openDialog(label, `${line('服务', r.name)}${line('影响范围', r.affectedScope)}<div class="state-banner warning">${esc(r.impact)}</div>${line('运行依赖', r.dependencies)}<label class="check-label"><input id="biz-confirm-check" type="checkbox">我已核对维护窗口、影响与恢复条件</label>${errorBox()}`, btn('确认' + label, 'confirm-intent', 'primary'), () => {
      if (!valid(s)) return;
      if (!operationsReady(p)) { fail('运维验证已过期，请重新进入运维工作区。'); return; }
      if (!$('biz-confirm-check')?.checked) { fail('请先核对影响范围。'); return; }
      s.used = true; closeDialog();
      const steps = willStop ? [{ status: '停止中', label: '等待服务停止' }] : restart ? [{ status: '停止中', label: '等待服务停止' }, { status: '启动中', label: '启动服务' }, { status: '检查中', label: '检查运行依赖' }] : [{ status: '启动中', label: '启动服务' }, { status: '检查中', label: '检查运行依赖' }];
      stateSequence(p, r, steps, () => {
        if (state.scenario === 'failed') { r.status = '恢复失败'; r.recovery = '依赖检查未通过'; commit(p, r, '服务恢复检查未通过'); return; }
        if (state.scenario === 'unknown') { r.status = '结果待核对'; r.recovery = '尚未收到恢复确认'; commit(p, r, '服务恢复结果待核对'); return; }
        r.status = willStop ? '已停止' : '运行中'; r.recovery = willStop ? '已停止' : '已在线'; r.lastChanged = now(); r.progress = 100; commit(p, r, label + '并完成状态核对');
        if (r.id === 'service-platform') { p.session = { status: '未登录', account: username(), verified: false }; proof = null; api.logout(); }
      });
    });
  }
  function filteredLogs(r) {
    const from = Date.parse(r.startTime.replace(' ', 'T') + ':00Z'), until = Date.parse(r.endTime.replace(' ', 'T') + ':00Z');
    const date = r.startTime.slice(0, 10);
    return (r.logLines || []).filter(text => {
      const time = Date.parse(date + 'T' + text.slice(0, 8) + 'Z');
      return time >= from && time <= until && (r.level === '全部级别' || (r.level === '仅 ERROR' ? /\bERROR\b/.test(text) : /\b(?:WARN|ERROR)\b/.test(text)));
    });
  }
  function logSnapshot(r) {
    r.packagedLines = filteredLogs(r); r.packagedAt = now();
    r.packageBytes = new TextEncoder().encode(r.packagedLines.join('\n')).length;
    r.result = `${r.packagedLines.length} 条记录 · ${r.packageBytes} B`; r.artifactReady = true;
  }
  function diagnosticRun(p, r) {
    const target = p.allowedTargets.find(t => t.name === r.target && t.type === r.diagnosticType);
    if (!target) { inspect('诊断目标不可用', [['原因', '请选择与诊断类型匹配的已登记目标。']]); return; }
    if (r.diagnosticType === '平台日志') {
      const start = Date.parse(r.startTime), end = Date.parse(r.endTime);
      if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start || end - start > 86400000 * 7) { inspect('请调整日志时间范围', [['要求', '结束时间晚于开始时间，单次最多选择7天。']]); return; }
      stateSequence(p, r, [{ status: '查询中', label: '查询运行日志' }, { status: '打包中', label: '生成诊断包' }], () => { logSnapshot(r); r.status = '已打包'; r.lastRun = now(); commit(p, r, '生成运行日志诊断包'); });
    } else {
      stateSequence(p, r, [{ status: '检查中', label: '解析目标地址' }, { status: '检查中', label: '检查目标连接' }], () => {
        const failed = state.scenario === 'failed'; r.status = failed ? '检查失败' : '已完成'; r.result = failed ? '目标端口未响应' : '连接成功 · 8 ms'; r.lastRun = now(); r.checks = failed ? ['DNS 解析通过', 'TCP 连接超时'] : ['DNS 解析通过', '目标端口可达', ...(r.target.endsWith(':443') ? ['证书校验通过'] : [])]; commit(p, r, '完成平台网络诊断');
      });
    }
  }
  function installationFingerprint(r) { return [r.target, r.deploymentMode, Number(r.nodeCount), Number(r.storageGB), r.releaseVersion].join('|'); }
  function installationProblems(p, r) {
    const problems = [];
    if (!p.fields.find(f => f.key === 'target').options.includes(r.target)) problems.push('目标设备未登记');
    if (Number(r.storageGB) < 100) problems.push('可用存储不足，本次部署配置需要 100 GB');
    if (!Number.isInteger(Number(r.nodeCount)) || Number(r.nodeCount) < (r.deploymentMode === '独立 API 与任务节点' ? 2 : 1)) problems.push('节点数量不满足部署方式');
    if (state.scenario === 'failed') problems.push('运行依赖检查未通过');
    return problems;
  }
  function installationPrecheck(p, r) {
    const s = snapshot(p, [r]); let step = 0, problems = [];
    const steps = ['环境', '配置', '预检', '结果'];
    function draw() {
      let body = step === 0 ? line('目标设备', r.target) + line('操作系统', r.os || 'Linux 容器宿主机') + line('运行依赖', r.database || 'PostgreSQL 17') : step === 1 ? line('部署方式', r.deploymentMode) + line('节点数量', r.nodeCount) + line('存储容量', r.storageGB + ' GB') + line('安装版本', r.releaseVersion) : step === 2 ? '<p>将检查设备登记、节点数量、可用存储与运行依赖。</p><div class="check-row">设备范围与部署配置已固定</div><div class="check-row">本次部署配置所需存储：100 GB</div>' : problems.length ? `<div class="state-banner warning">环境检查未通过</div><ul>${problems.map(x => `<li>${esc(x)}</li>`).join('')}</ul><p>调整配置后重新检查，尚未开始安装。</p>` : '<div class="state-banner info">环境检查通过</div><p>可以在确认维护影响后开始安装。</p>';
      openDialog('部署环境检查', `<div class="wizard-steps">${steps.map((x, i) => `<span class="${i === step ? 'current' : ''}">${i + 1} ${x}</span>`).join('')}</div>${body}${errorBox()}`, btn(step === 3 ? '完成' : step === 2 ? '运行预检' : '下一步', 'confirm-intent', 'primary'), () => {
        if (!valid(s)) return;
        if (step === 3) { s.used = true; closeDialog(); render(); return; }
        if (step === 2) {
          problems = installationProblems(p, r); r.precheckPassed = problems.length === 0; r.precheckFingerprint = problems.length ? null : installationFingerprint(r); r.status = problems.length ? '检查未通过' : '预检通过'; r.stage = problems.length ? '环境检查失败' : '准备安装'; r.precheckResults = problems.length ? problems : ['设备已登记', '节点数量满足要求', '存储容量满足要求', '运行依赖可用']; commit(p, r, problems.length ? '部署预检未通过' : '部署预检通过'); s.rows[0].version = r.version;
        }
        step++; draw();
      });
    }
    draw();
  }
  function startInstall(p, r) {
    const problems = installationProblems(p, r);
    if (!r.precheckPassed || r.precheckFingerprint !== installationFingerprint(r) || problems.length) { inspect('安装条件未满足', [['部署', r.name], ['原因', problems.join('；') || '请先运行当前配置的环境检查。']]); return; }
    const s = snapshot(p, [r]);
    openDialog('确认安装范围', `${line('部署名称', r.name)}${line('目标设备', r.target)}${line('版本', r.releaseVersion)}<div class="state-banner warning">${esc(r.impact)}</div><label class="check-label"><input id="biz-confirm-check" type="checkbox">已核对设备、维护窗口和数据保留条件</label>${errorBox()}`, btn('开始安装', 'confirm-intent', 'primary'), () => {
      if (!valid(s)) return;
      if (!$('biz-confirm-check')?.checked) { fail('请确认安装范围与影响。'); return; }
      if (!r.precheckPassed || r.precheckFingerprint !== installationFingerprint(r) || installationProblems(p, r).length) { fail('预检已失效，请重新检查。'); return; }
      s.used = true; closeDialog();
      stateSequence(p, r, [{ status: '安装中', label: '准备运行依赖' }, { status: '安装中', label: '准备存储' }, { status: '安装中', label: '安装平台服务' }, { status: '验证中', label: '核对服务运行状态' }], () => {
        r.status = '已完成'; r.stage = '运行核对'; r.progress = 100; r.steps.forEach(x => { x.status = '已完成'; }); commit(p, r, '完成部署并核对运行结果');
      });
    });
  }
  const auditFilters = { auditType: '全部类型', actor: '全部操作人', visibility: '可见记录', result: '全部结果', startTime: '', endTime: '' };
  function auditFilter(p) {
    const extra = { key: 'result', label: '执行结果', type: 'select', options: ['全部结果', '成功', '已拒绝', '失败'] };
    const s = snapshot(p, [], false), fields = [...p.fields.map(f => f.key === 'actor' ? { ...f, options: ['全部操作人', ...new Set(all(p.id).map(r => r.actor))] } : f), extra];
    openDialog('筛选审计记录', `<div class="form-grid">${fields.map(f => fieldMarkup(f, auditFilters)).join('')}</div>${errorBox()}`, btn('应用筛选', 'confirm-intent', 'primary'), () => {
      if (!valid(s)) return;
      const vals = readFields(fields); if (!vals) return;
      if ((vals.startTime && !Number.isFinite(Date.parse(vals.startTime))) || (vals.endTime && !Number.isFinite(Date.parse(vals.endTime))) || (vals.startTime && vals.endTime && Date.parse(vals.startTime) > Date.parse(vals.endTime))) { fail('请填写有效的起止时间。'); return; }
      Object.assign(auditFilters, vals); s.used = true; closeDialog(); render();
    });
  }
  function permissionDetail(p, r) {
    inspect(r.name, [['角色说明', r.description], ['成员数量', r.memberCount], ['资源组', r.resourceGroup]], p.permissionMatrix.map(g => `<section><h3>${esc(g.title)}</h3>${g.items.map(i => line(i.label, r[i.key] ? '允许' : '拒绝')).join('')}</section>`).join(''), button(p, r, { label: '编辑权限', verb: 'edit', intent: 'edit_permissions' }));
  }
  function action(p, r, a, records) {
    if (p.section !== 'admin') return false;
    if (!ctx.allowed(p) || !ctx.canAction(p, a)) { inspect('没有访问权限', [['原因', '当前身份没有此管理功能的访问权限。']]); return true; }
    activeAction = { page: p.id, action: structuredClone(a) };
    const intent = a.intent || a.verb, targets = r ? [r] : records || [];
    const readOnly = ['inspect', 'sessions', 'role_members', 'authorization_preview', 'license_preview', 'service_recovery', 'diagnostic_result', 'installation_steps', 'audit_filter'].includes(intent);
    if (!readOnly && !canChange(p)) { inspect('操作不可用', [['原因', '当前身份没有此操作的权限。']]); return true; }
    if (r && !get(p, r.id)) { inspect('记录不可用', [['原因', '当前身份或范围不能访问此对象。']]); return true; }
    if (a.appliesTo && (!r || !a.appliesTo.includes(r.id))) { inspect('操作不可用', [['原因', '此操作不适用于当前设置项。']]); return true; }
    if (a.excludeIds?.includes(r?.id)) { inspect('操作不可用', [['原因', '此目标不支持所选操作。']]); return true; }
    if (intent === 'inspect') {
      if (!r) return true;
      if (p.special === 'permissions') { permissionDetail(p, r); return true; }
      if (p.special === 'system') { inspect('系统资源详情', [['节点', r.name], ['组件类型', r.type], ['状态', r.status], ['版本', r.releaseVersion], ['CPU', r.cpu], ['内存', r.memory], ['存储', r.storage], ['采样时间', r.observedAt], ['运行时间', r.uptime], ['最近异常', r.lastError]]); return true; }
      if (p.special === 'license') { licensePreview(p, r); return true; }
      if (p.special === 'audit') { inspect('操作审计详情', [['操作', r.name], ['操作人', r.actor], ['类型', r.auditType], ['对象', r.target], ['结果', r.result], ['发生时间', r.time], ['变更内容', r.changes], ['关联编号', r.requestId], ['记录状态', r.status]]); return true; }
      if (p.special === 'profile') { inspect(r.name, fieldsFor(p, r).map(f => [f.label, r[f.key]]), '', button(p, r, { label: '编辑', verb: 'edit', intent: 'edit_profile' })); return true; }
      if (p.special === 'install') return action(p, r, { ...a, intent: 'installation_steps' }, targets);
      if (p.special === 'diagnostics') return action(p, r, { ...a, intent: 'diagnostic_result' }, targets);
      if (p.special === 'operations') return action(p, r, { ...a, intent: 'service_recovery' }, targets);
      if (p.special === 'integrations') return action(p, r, { ...a, intent: 'authorization_preview' }, targets);
      if (p.special === 'mfa') { inspect('认证详情', [['账号', r.name], ['验证方式', r.method], ['状态', r.status], ['验证要求', r.requirement], ['绑定时间', r.boundAt], ['最近验证', r.lastVerified]], '', p.rowActions.map(x => button(p, r, x)).join('')); return true; }
      return false;
    }
    if (intent === 'edit_profile') {
      const target = r || visible(p)[0]; if (!target) return true;
      const intro = target.fieldKeys.includes('avatar') ? `<div class="avatar-choices">${p.avatarPresets.map(x => `<span class="profile-avatar small ${avatarClass(x.name)}" aria-label="${esc(x.name)}">${esc(x.initial)}</span>`).join('')}</div>` : '';
      form(p, target, '编辑' + target.name, fieldsFor(p, target), target, values => { if (values.email && !/^[a-z0-9._+-]+@example\.test$/i.test(values.email)) { fail('请使用 example.test 邮箱。'); return false; } Object.assign(target, values); saved(p, target, values); complete(p, target, '更新个人资料'); }, intro); return true;
    }
    if (intent === 'select_account') { choose(p, 'accountRef', target => loginFlow(p, target)); return true; }
    if (intent === 'session_toggle') {
      if (r.status === '已登录') frozenApply(p, [r], { ...a, label: '退出账号' }, () => { r.status = '已退出'; r.sessions = '已失效'; r.loginVerified = false; commit(p, r, '退出账号'); proof = null; api.logout(); });
      else loginFlow(p, r); return true;
    }
    if (intent === 'password_change' || intent === 'admin_password_reset') { passwordChange(p, r, intent === 'admin_password_reset'); return true; }
    if (intent === 'sessions') { inspect('活动会话', [['账号', r.name], ['会话状态', r.sessions], ['最近登录', r.lastLogin], ['密码状态', r.passwordState], ['认证状态', r.mfa]]); return true; }
    if (intent === 'select_mfa_account') { choose(p, 'accountRef', target => action(p, target, { intent: 'inspect', verb: 'inspect' }, [target])); return true; }
    if (intent === 'mfa_toggle') {
      if (!r.enabled) mfaFlow(p, r);
      else requireProof(p, [r], () => frozenApply(p, [r], { ...a, label: '停用多因素认证' }, () => { r.enabled = false; r.verified = false; r.status = '未启用'; proof = null; commit(p, r, '停用多因素认证'); })); return true;
    }
    if (intent === 'mfa_verify') {
      if (!r.enabled) { inspect('尚未启用认证', [['账号', r.name], ['下一步', '请先关联身份验证器。']], '', button(p, r, { label: '启用认证', verb: 'toggle', intent: 'mfa_toggle' }, 'primary')); return true; }
      const s = snapshot(p, [r]); openDialog('验证身份', `${line('账号', r.name)}${line('验证方式', r.method)}<label class="check-label"><input id="admin-mfa-check" type="checkbox">完成本次身份验证</label>${errorBox()}`, btn('完成验证', 'confirm-intent', 'primary'), () => { if (!valid(s)) return; if (!$('admin-mfa-check')?.checked) { fail('请完成本次身份验证。'); return; } s.used = true; r.verified = true; r.lastVerified = now(); if (r.name === username()) proof = { epoch: state.epoch, role: state.role, domain: state.domain, at: Date.now() }; complete(p, r, '完成多因素身份验证'); }); return true;
    }
    if (intent === 'mfa_reset') { requireProof(p, [r], () => frozenApply(p, [r], a, () => { r.enabled = false; r.verified = false; r.status = '未绑定'; r.boundAt = '未绑定'; r.lastVerified = '尚未验证'; proof = null; commit(p, r, '重置多因素认证绑定'); })); return true; }
    if (p.special === 'users' && (['create', 'edit'].includes(intent) || intent === 'assign_roles')) { if (intent === 'assign_roles' && !targets.length) { inspect('尚未选择用户', [['下一步', '请先选择需要分配角色的用户。']]); return true; } editUsers(p, r, intent === 'assign_roles', intent === 'assign_roles' ? targets : r ? [r] : []); return true; }
    if (['user_toggle', 'disable_users', 'delete_user', 'restore_user'].includes(intent)) {
      if (!targets.length) { inspect('尚未选择用户', [['下一步', '请先选择用户。']]); return true; }
      const blocked = targets.find(t => isSelf(t) || t.lastAdministrator);
      if (blocked) { inspect('账号操作受限', [['用户', blocked.name], ['原因', isSelf(blocked) ? '不能停用或删除当前登录账号。' : '不能停用或删除最后一个平台管理员。']]); return true; }
      requireProof(p, targets, () => frozenApply(p, targets, a, () => { targets.forEach(t => { if (intent === 'restore_user' || (intent === 'delete_user' && t.archived)) { t.archived = false; t.status = '停用'; } else if (intent === 'delete_user') { t.archived = true; t.status = '已归档'; } else t.status = intent === 'disable_users' || t.status === '启用' ? '停用' : '启用'; t.sessions = '已撤销'; commit(p, t, a.label); }); updateAccess(); })); return true;
    }
    if (p.special === 'permissions' && ['create', 'edit_permissions', 'edit'].includes(intent)) { editRole(p, r); return true; }
    if (intent === 'role_members') { const members = all('admin/users').filter(u => (r.members || []).includes(u.id) && !u.archived); inspect(r.name + ' · 角色成员', [['成员数量', members.length]], members.map(u => line(u.name, u.username + ' · ' + u.status)).join('') || '<p>此角色暂无成员。</p>'); return true; }
    if (intent === 'delete_role') { const problem = roleMutationBlocked(r); if (problem || r.lastAdministrator || (r.members || []).length) { inspect('角色不能删除', [['角色', r.name], ['原因', problem || (r.lastAdministrator ? '必须保留最后一个平台管理员角色。' : '请先调整此角色关联的用户。')]]); return true; } requireProof(p, [r], () => frozenApply(p, [r], a, () => { r.archived = !r.archived; r.status = r.archived ? '已归档' : '启用'; commit(p, r, r.archived ? '归档角色' : '还原角色'); updateAccess(); })); return true; }
    if (p.special === 'resources' && ['create', 'edit', 'edit_resource', 'associate_roles'].includes(intent)) { editResource(p, r, intent === 'associate_roles'); return true; }
    if (intent === 'delete_resource') { if ((r.roleIds || []).length) { inspect('资源组仍有关联角色', [['资源组', r.name], ['下一步', '请先解除角色关联，再删除资源组。']]); return true; } requireProof(p, [r], () => frozenApply(p, [r], a, () => { r.archived = !r.archived; r.status = r.archived ? '已归档' : '未分配'; commit(p, r, r.archived ? '归档资源组' : '还原资源组'); updateAccess(); })); return true; }
    if (intent === 'tenant_settings') { requireProof(p, [], () => form(p, null, '租户设置', p.tenantFields, { ...p.tenantSettings, tenantName: p.tenantSettings.name }, values => { const oldName = p.tenantSettings.name; Object.assign(p.tenantSettings, values, { name: values.tenantName }); all(p.id).filter(x => x.tenant === oldName).forEach(x => { x.tenant = values.tenantName; commit(p, x, '更新租户配置'); }); api.audit('更新租户配置', values.tenantName, '成功', 'platform'); closeDialog(); render(); })); return true; }
    if (intent === 'audit_filter') { auditFilter(p); return true; }
    if (intent === 'audit_export') { const rows = targets.filter(x => get(p, x.id)); if (!rows.length) { inspect('没有可导出的记录', [['下一步', '调整筛选条件后重试。']]); return true; } downloaded(p, rows, 'ADTR-audit.txt', rows.map(x => [x.time, x.actor, x.auditType, x.name, x.target, x.result, x.changes, x.requestId].join(' | ')).join('\n')); return true; }
    if (intent === 'audit_archive') { if (!targets.length) return true; frozenApply(p, targets, a, () => targets.forEach(t => { t.archived = !t.archived; t.status = t.archived ? '已归档' : '可见'; commit(p, t, t.archived ? '归档平台审计记录' : '还原平台审计记录'); })); return true; }
    if (p.special === 'integrations' && ['create', 'edit'].includes(intent)) {
      form(p, r, r ? '编辑访问授权' : '登记第三方应用', p.fields, r || {}, values => {
        if (!Number.isFinite(Date.parse(values.expires))) { fail('请填写有效的授权到期日。'); return false; }
        const target = r || newRow(p, { callback: '无回调', lastUsed: '尚未访问', authorizationRef: 'grant-local-' + serial });
        Object.assign(target, values, { enabled: false, status: '待授权' }); complete(p, target, '保存第三方应用并等待授权');
      }); return true;
    }
    if (intent === 'authorization_preview') {
      inspect('应用授权流程', [['应用', r.name], ['接入方式', r.provider], ['请求权限', r.permission], ['凭据引用', r.credentialRef], ['回调地址', r.callback], ['到期日', r.expires], ['状态', r.status]], `<ol class="evidence-list">${p.authorizationSteps.map(x => `<li>${esc(x)}</li>`).join('')}</ol>`, button(p, r, { label: r.enabled ? '撤销授权' : '确认授权', verb: 'toggle', intent: 'integration_toggle' }, 'primary')); return true;
    }
    if (intent === 'integration_toggle') {
      if (!r.enabled && Date.parse(r.expires + 'T23:59:59Z') < dateNow().getTime()) { inspect('授权已过期', [['应用', r.name], ['到期日', r.expires], ['下一步', '请先更新有效期后重新授权。']]); return true; }
      requireProof(p, [r], () => frozenApply(p, [r], { ...a, label: r.enabled ? '撤销应用授权' : '确认应用授权' }, () => { r.enabled = !r.enabled; r.status = r.enabled ? '已授权' : '已撤销'; r.authorizedAt = r.enabled ? now() : r.authorizedAt; commit(p, r, r.enabled ? '授予第三方应用访问权限' : '撤销第三方应用授权'); })); return true;
    }
    if (intent === 'integration_test') { const expired = Date.parse(r.expires + 'T23:59:59Z') < dateNow().getTime(); r.authorizationCheck = expired ? '授权已过期' : !r.enabled ? '尚未授权' : '引用与授权有效'; r.checkedAt = now(); if (expired) { r.status = '已过期'; r.enabled = false; } commit(p, r, '检查应用授权状态'); inspect('授权检查结果', [['应用', r.name], ['检查结果', r.authorizationCheck], ['凭据引用', r.credentialRef], ['检查时间', r.checkedAt]]); render(); return true; }
    if (intent === 'storage_settings') { requireProof(p, [], () => form(p, null, '存储管理', p.fields, p.settings, values => { Object.assign(p.settings, values); const node = all(p.id)[0]; commit(p, node, '更新存储管理策略'); const section = p.sections.find(x => x.title === '存储策略'); if (section) section.items = [{ label: '容量告警', value: values.storageWarning + '%' }, { label: '审计保留期', value: values.auditRetention + ' 天' }, { label: '诊断包保留期', value: values.diagnosticRetention + ' 天' }]; closeDialog(); render(); })); return true; }
    if (intent === 'refresh_health') { if (r.status !== '未配置') { r.observedAt = now(); r.lastRefresh = now(); commit(p, r, '刷新节点资源观测'); } inspect('资源观测', [['节点', r.name], ['状态', r.status], ['CPU', r.cpu], ['内存', r.memory], ['存储', r.storage], ['采样时间', r.observedAt], ['最近异常', r.lastError]]); render(); return true; }
    if (intent === 'license_import') { form(p, null, '选择许可文件', p.fields, {}, values => { const candidate = all(p.id).find(x => x.packageRef === values.packageRef); if (!candidate) { fail('许可文件不可用。'); return false; } candidate.packageReady = true; commit(p, candidate, '读取候选许可'); closeDialog(); licensePreview(p, candidate); }); return true; }
    if (intent === 'license_preview') { licensePreview(p, r); return true; }
    if (intent === 'license_validate') { const errors = licenseCheck(p, r); r.validation = errors.length ? errors.join('；') : '通过'; r.checkedAt = now(); if (!r.active) r.status = errors.length ? '校验失败' : '待激活'; commit(p, r, '校验候选许可'); licensePreview(p, r); render(); return true; }
    if (intent === 'license_activate') {
      const errors = licenseCheck(p, r); if (errors.length) { r.validation = errors.join('；'); r.status = '校验失败'; commit(p, r, '许可激活校验未通过'); licensePreview(p, r); render(); return true; }
      requireProof(p, [r], () => frozenApply(p, [r], a, () => { const latest = licenseCheck(p, r); if (latest.length) { r.validation = latest.join('；'); r.status = '校验失败'; commit(p, r, '许可激活校验未通过'); return; } all(p.id).forEach(other => { if (other.active && other.id !== r.id) { other.active = false; other.status = '已替换'; commit(p, other, '替换当前许可'); } }); r.active = true; r.validation = '通过'; r.status = '已激活'; r.activatedAt = now(); commit(p, r, '激活许可'); p.stats[0].value = r.licenseType; p.stats[0].detail = '有效至 ' + r.expires; p.stats[1].value = p.currentUsage.domains + ' / ' + r.domainsAllowed; })); return true;
    }
    if (intent === 'edit_settings') {
      const target = r || visible(p)[0]; if (!target) return true;
      requireProof(p, [target], () => form(p, target, '编辑' + target.name, fieldsFor(p, target), target, values => { if (values.manualTime && !Number.isFinite(Date.parse(values.manualTime))) { fail('请填写有效的系统时间。'); return false; } Object.assign(target, values); saved(p, target, values); complete(p, target, '更新系统设置'); })); return true;
    }
    if (intent === 'sync_time') { if (r.id !== 'settings-time' || !r.ntpEnabled) { inspect('时间同步未启用', [['下一步', '启用自动同步并选择时间源后重试。']]); return true; } requireProof(p, [r], () => { r.lastSynced = now(); r.offset = '0 ms'; complete(p, r, '完成平台时间同步'); }); return true; }
    if (intent === 'operations_login') { operationsLogin(p); return true; }
    if (intent === 'operations_logout') { p.session = { status: '未登录', account: username(), verified: false }; proof = null; api.audit('退出运维工作区', username(), '成功', 'platform'); appendAudit('退出运维工作区', username()); closeDialog(); render(); return true; }
    if (intent === 'service_toggle' || intent === 'service_restart') { serviceChange(p, r, intent === 'service_restart'); return true; }
    if (intent === 'service_recovery') { inspect('服务恢复状态', [['服务', r.name], ['状态', r.status], ['影响范围', r.affectedScope], ['恢复阶段', r.recovery], ['最近变更', r.lastChanged]], `<div class="timeline">${(r.history || []).map(h => `<div class="timeline-item"><small>${esc(h.time)}</small><p>${esc(h.text)}</p></div>`).join('') || '<p>最近没有服务变更。</p>'}</div>`); return true; }
    if (intent === 'diagnostic_run') { diagnosticRun(p, r); return true; }
    if (intent === 'diagnostic_result') { const logs = r.diagnosticType === '平台日志'; inspect('诊断结果', [['项目', r.name], ['目标', r.target], ['状态', r.status], ['结果', r.result], ['执行时间', r.lastRun]], logs ? `<h3>运行日志</h3><pre class="evidence">${esc((r.artifactReady ? r.packagedLines : filteredLogs(r)).join('\n') || '当前筛选没有匹配日志。')}</pre>` : `<ol class="evidence-list">${(r.checks || []).map(x => `<li>${esc(x)}</li>`).join('')}</ol>`, r.artifactReady ? button(p, r, { label: '下载诊断包', verb: 'download', intent: 'diagnostic_download' }) : ''); return true; }
    if (intent === 'diagnostic_download') { if (!r.artifactReady || !Array.isArray(r.packagedLines)) { inspect('诊断包尚未就绪', [['下一步', '先查询日志并完成打包。']]); return true; } downloaded(p, [r], r.artifactName || 'ADTR-diagnostics.txt', [`项目：${r.name}`, `服务：${r.target}`, `时间：${r.startTime} 至 ${r.endTime}`, `日志级别：${r.level}`, `结果：${r.result}`, '', ...r.packagedLines].join('\n')); return true; }
    if (intent === 'installation_precheck') { installationPrecheck(p, r); return true; }
    if (intent === 'installation_start') { startInstall(p, r); return true; }
    if (intent === 'installation_steps') { inspect('部署过程', [['部署', r.name], ['目标', r.target], ['版本', r.releaseVersion], ['状态', r.status], ['当前阶段', r.stage], ['进度', r.progress + '%']], `<ol class="evidence-list">${(r.steps || []).map(x => `<li>${esc(x.label)} · ${esc(x.status)}</li>`).join('')}</ol>${r.precheckResults ? '<h3>预检结果</h3><ul>' + r.precheckResults.map(x => '<li>' + esc(x) + '</li>').join('') + '</ul>' : ''}`); return true; }
    if (intent === 'installation_record') { downloaded(p, [r], 'ADTR-installation-' + r.id + '.txt', [`部署：${r.name}`, `目标：${r.target}`, `版本：${r.releaseVersion}`, `状态：${r.status}`, ...(r.steps || []).map(x => x.label + '：' + x.status), ...(r.history || []).map(x => x.time + ' ' + x.text)].join('\n')); return true; }
    return false;
  }
  function saved(p, r, values) {
    for (const f of p.fields || []) if (f.type === 'number' && values[f.key] != null) r[f.key] = Number(values[f.key]);
    if (p.special === 'profile') {
      r.summary = r.fieldKeys.includes('displayName') ? `${r.displayName} · ${r.team}` : r.fieldKeys.includes('avatar') ? `${r.avatar}头像 · ${r.language}` : `${r.timezone} · ${r.timeFormat}`;
      if (values.avatar) { const preset = p.avatarPresets.find(x => x.name === values.avatar); if (preset) { state.profileAvatars ||= {}; state.profileAvatars[state.role] = { ...structuredClone(preset), className: avatarClass(preset.name) }; } }
    }
    if (p.special === 'settings') {
      r.summary = r.id === 'settings-brand' ? `${r.systemName} · ${r.logoPreset}` : r.id === 'settings-time' ? `${r.timezone} · ${r.ntpEnabled ? '自动同步' : '手动设置'}` : `${r.sessionTimeout} 分钟会话 · ${r.maintenanceMode ? '维护模式' : '常规运行'}`;
      if (r.id === 'settings-brand') state.systemName = r.systemName;
    }
    if (p.special === 'diagnostics') {
      const source = all(p.id).find(x => x.id !== r.id && x.target === r.target && x.diagnosticType === r.diagnosticType);
      r.logLines = structuredClone(source?.logLines || []); r.checks = []; r.artifactReady = false; r.packagedLines = undefined; r.status = '待执行'; r.result = '尚未执行'; r.lastRun = '尚未执行'; r.artifactName = 'ADTR-diagnostics-' + r.id + '.txt';
    }
    if (p.special === 'install') {
      r.precheckPassed = false; r.precheckFingerprint = null; r.stage = '环境检查'; r.status = '待检查'; r.progress = 0; r.os = 'Linux 容器宿主机'; r.database = 'PostgreSQL 17'; r.impact = '仅安装到所选已登记设备。已有部署需核对维护窗口与数据保留条件。'; r.steps = ['确认设备', '检查依赖', '准备存储', '安装服务', '验证运行'].map(label => ({ label, status: '待执行' }));
    }
  }
  function panel(p, rows) {
    if (p.section !== 'admin' || (!rows.length && p.special !== 'audit')) return '';
    if (p.special === 'profile') {
      const user = rows.find(x => x.fieldKeys.includes('displayName')), avatarRow = rows.find(x => x.fieldKeys.includes('avatar')), preset = p.avatarPresets.find(x => x.name === avatarRow?.avatar) || p.avatarPresets[0];
      return `<section class="panel business-feature"><div class="profile-banner"><div class="profile-avatar ${avatarClass(preset.name)}">${esc((user?.displayName || '用').slice(0, 1))}</div><div><h2>${esc(user?.displayName || username())}</h2><p class="subtitle">${esc(user?.email || '')}</p>${tag(username(), 'outline')}</div></div><div class="panel-body">${rows.map(x => `<div class="info-row"><span>${esc(x.name)}</span><strong>${esc(x.summary)}</strong>${button(p, x, { label: '编辑', verb: 'edit', intent: 'edit_profile' })}</div>`).join('')}</div></section>`;
    }
    if (p.special === 'mfa') {
      const current = rows.find(x => x.name === username()) || rows[0];
      return `<section class="panel business-feature"><div class="panel-head"><div><h2>登录保护</h2><p class="panel-subtitle">${esc(current.name)}</p></div>${tag(current.status, current.enabled ? 'green' : 'amber')}</div><div class="mfa-steps"><div class="security-shield">${icon('lock')}</div><div><h3>${current.enabled ? '身份验证器已启用' : '添加额外登录保护'}</h3><p>${esc(current.requirement)}</p><div class="pipeline"><span>1 关联验证器</span><b>→</b><span>2 验证身份</span><b>→</b><span>3 确认保护</span></div></div></div><div class="panel-body">${line('最近验证', current.lastVerified)}${button(p, current, { label: current.enabled ? '验证身份' : '启用认证', verb: current.enabled ? 'test' : 'toggle', intent: current.enabled ? 'mfa_verify' : 'mfa_toggle' }, 'primary')}</div></section>`;
    }
    if (p.special === 'account-security') return `<section class="panel business-feature"><div class="panel-head"><h2>账号安全状态</h2>${tag(username(), 'outline')}</div><div class="readiness-grid panel-body"><div>${icon('user')}${esc(username())}</div><div>${icon('clock')}密码有效期 90 天</div><div>${icon('lock')}敏感操作需要验证</div></div></section>`;
    if (p.special === 'permissions') return `<section class="panel business-feature"><div class="panel-head"><h2>角色功能权限</h2><p>功能操作与域范围分别授权</p></div><div class="table-scroll"><table><thead><tr><th>功能组</th>${rows.map(r => `<th>${esc(r.name)}</th>`).join('')}</tr></thead><tbody>${p.permissionMatrix.map(g => `<tr><td>${esc(g.title)}</td>${rows.map(r => `<td>${g.items.filter(i => r[i.key]).length} / ${g.items.length} 项允许</td>`).join('')}</tr>`).join('')}</tbody></table></div></section>`;
    if (p.special === 'operations') {
      const active = operationsReady(p);
      return `<section class="panel business-feature"><div class="panel-head"><div><h2>运维会话</h2><p class="panel-subtitle">${esc(username())}</p></div>${tag(active ? '已验证' : '需要验证', active ? 'green' : 'amber')}</div><div class="panel-body">${line('操作范围', '平台服务与运行配置')}${line('验证有效期', active ? '当前会话有效' : '尚未建立或已过期')}${button(p, null, { label: active ? '退出运维' : '进入运维', verb: active ? 'toggle' : 'activate', intent: active ? 'operations_logout' : 'operations_login' }, active ? '' : 'primary')}</div></section>`;
    }
    if (p.special === 'install') return `<section class="panel business-feature"><div class="panel-head"><div><h2>部署准备</h2><p class="panel-subtitle">先核对设备与依赖，再确认维护影响</p></div>${tag('Linux 容器宿主机', 'outline')}</div><div class="pipeline"><span>1 环境</span><b>→</b><span>2 配置</span><b>→</b><span>3 预检</span><b>→</b><span>4 结果</span></div><div class="readiness-grid panel-body"><div>${icon('server')}已登记设备</div><div>${icon('layers')}PostgreSQL 17</div><div>${icon('clock')}分步骤查看运行结果</div></div></section>`;
    if (p.special === 'audit') return `<section class="panel business-feature"><div class="panel-head"><h2>查询范围</h2>${button(p, null, { label: '筛选记录', verb: 'inspect', intent: 'audit_filter' })}</div><div class="panel-body inline">${tag(auditFilters.auditType, 'outline')}${tag(auditFilters.actor, 'outline')}${tag(auditFilters.visibility, 'blue')}${tag(auditFilters.result, 'outline')}</div></section>`;
    if (p.special === 'license') { const active = rows.find(r => r.active); return `<section class="panel business-feature"><div class="panel-head"><h2>当前授权</h2>${tag(active?.status || '未激活', active ? 'green' : 'amber')}</div><div class="panel-body">${line('许可', active?.name || '尚未激活')}${line('授权范围', active?.modules)}${line('设备标识', p.currentDevice)}${line('许可有效期', active?.expires)}</div></section>`; }
    return '';
  }

  for (const p of Object.values(pages).filter(x => x.section === 'admin')) {
    for (const r of all(p.id)) if (typeof r.version === 'string') { r.releaseVersion = r.version; r.version = 1; }
    if (p.special === 'install') {
      p.columns = p.columns.map(c => c.key === 'version' ? { ...c, key: 'releaseVersion' } : c);
      p.fields = p.fields.map(f => f.key === 'version' ? { ...f, key: 'releaseVersion' } : f);
      for (const r of all(p.id)) if (r.precheckPassed) r.precheckFingerprint = installationFingerprint(r);
    }
  }
  const profilePage = page('profile');
  if (profilePage) {
    const seeds = structuredClone(all(profilePage.id));
    const profileOwners = [...all('admin/users'), ...all('admin/accounts').filter(a => ['demo.new', 'demo.expired'].includes(a.name)).map(a => ({ id: a.id, username: a.name, name: a.owner, email: a.name + '@example.test', role: '待授权' }))];
    memory[profilePage.id] = profileOwners.flatMap(u => seeds.map(seed => {
      const r = { ...structuredClone(seed), id: seed.id + '-' + u.id, ownerAccount: u.username, version: 1 };
      if (r.fieldKeys.includes('displayName')) { r.displayName = u.name; r.email = u.email; r.account = u.username; r.team = u.role === '待授权' ? '待授权' : u.role === '运维管理员' ? '基础设施团队' : '安全运营中心'; r.summary = r.displayName + ' · ' + r.team; }
      return r;
    }));
    profilePage.filterRecord = r => r.ownerAccount === username();
  }
  for (const u of all('admin/users')) {
    if (!all('admin/accounts').some(r => r.name === u.username)) all('admin/accounts').push({ id: 'account-' + u.id, domain: 'platform', name: u.username, owner: u.name, status: '待验证', passwordState: '有效', lastLogin: u.lastLogin, expires: '2027-01-08', sessions: '无', authFlow: 'active', mfa: u.mfa, version: 1 });
    if (!all('admin/mfa').some(r => r.name === u.username)) all('admin/mfa').push({ id: 'mfa-' + u.id, domain: 'platform', name: u.username, method: '身份验证器', status: u.mfa === '已启用' ? '已启用' : '未绑定', enabled: u.mfa === '已启用', verified: false, requirement: '登录与敏感操作', boundAt: '2026-09-12 10:00 UTC', lastVerified: '尚未验证', version: 1 });
  }
  for (const a of all('admin/accounts')) {
    if (!all('admin/mfa').some(r => r.name === a.name)) all('admin/mfa').push({ id: 'mfa-' + a.id, domain: 'platform', name: a.name, method: '身份验证器', status: a.mfa === '已启用' ? '已启用' : '未绑定', enabled: a.mfa === '已启用', verified: false, requirement: '登录与敏感操作', boundAt: a.mfa === '已启用' ? '2026-09-12 10:00 UTC' : '未绑定', lastVerified: '尚未验证', version: 1 });
  }
  if (page('accounts')) page('accounts').filterRecord = r => api.permission('userManage') || r.name === username();
  if (page('mfa')) page('mfa').filterRecord = r => api.permission('userManage') || r.name === username();
  if (page('audit')) page('audit').filterRecord = r => {
    if (auditFilters.visibility === '可见记录' && r.archived) return false;
    if (auditFilters.visibility === '已归档记录' && !r.archived) return false;
    if (auditFilters.auditType !== '全部类型' && r.auditType !== auditFilters.auditType) return false;
    if (auditFilters.actor !== '全部操作人' && r.actor !== auditFilters.actor) return false;
    if (auditFilters.result !== '全部结果' && r.result !== auditFilters.result) return false;
    const t = Date.parse(r.time);
    return (!auditFilters.startTime || t >= Date.parse(auditFilters.startTime)) && (!auditFilters.endTime || t <= Date.parse(auditFilters.endTime));
  };
  for (const r of all('admin/diagnostics')) if (r.artifactReady && r.diagnosticType === '平台日志') logSnapshot(r);
  updateAccess();
  return { action, saved, panel, resumeSession };
};
