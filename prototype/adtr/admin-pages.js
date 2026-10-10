window.ADTR_ADMIN_PAGES = [
  {
    "id": "admin/profile",
    "section": "admin",
    "title": "个人资料",
    "subtitle": "管理个人信息、头像与工作偏好。",
    "scope": "platform",
    "kind": "settings",
    "columns": [
      {
        "key": "name",
        "label": "设置项目"
      },
      {
        "key": "summary",
        "label": "当前设置"
      },
      {
        "key": "updated",
        "label": "更新时间"
      }
    ],
    "rows": [
      {
        "id": "profile-personal",
        "domain": "platform",
        "name": "基本资料",
        "summary": "林青 · 安全运营中心",
        "displayName": "林青",
        "account": "demo.operator",
        "email": "demo.operator@example.test",
        "team": "安全运营中心",
        "updated": "2026-10-10 06:35 UTC",
        "fieldKeys": [
          "displayName",
          "email",
          "team"
        ]
      },
      {
        "id": "profile-avatar",
        "domain": "platform",
        "name": "头像与显示",
        "summary": "青绿头像 · 简体中文",
        "avatar": "青绿",
        "language": "简体中文",
        "updated": "2026-10-10 06:35 UTC",
        "fieldKeys": [
          "avatar",
          "language"
        ]
      },
      {
        "id": "profile-preferences",
        "domain": "platform",
        "name": "工作偏好",
        "summary": "Asia/Shanghai · 24 小时制",
        "timezone": "Asia/Shanghai",
        "timeFormat": "24 小时制",
        "landingPage": "安全总览",
        "updated": "2026-10-10 06:35 UTC",
        "fieldKeys": [
          "timezone",
          "timeFormat",
          "landingPage"
        ]
      }
    ],
    "fields": [
      {
        "key": "displayName",
        "label": "显示名称",
        "type": "text",
        "required": true
      },
      {
        "key": "email",
        "label": "联系邮箱",
        "type": "text"
      },
      {
        "key": "team",
        "label": "部门",
        "type": "text"
      },
      {
        "key": "avatar",
        "label": "头像样式",
        "type": "select",
        "options": [
          "青绿",
          "靛蓝",
          "暖橙",
          "石墨"
        ]
      },
      {
        "key": "language",
        "label": "语言",
        "type": "select",
        "options": [
          "简体中文",
          "English"
        ]
      },
      {
        "key": "timezone",
        "label": "时区",
        "type": "select",
        "options": [
          "Asia/Shanghai",
          "UTC",
          "Asia/Singapore"
        ]
      },
      {
        "key": "timeFormat",
        "label": "时间格式",
        "type": "select",
        "options": [
          "24 小时制",
          "12 小时制"
        ]
      },
      {
        "key": "landingPage",
        "label": "默认打开",
        "type": "select",
        "options": [
          "安全总览",
          "威胁调查",
          "任务与消息"
        ]
      }
    ],
    "primary": {
      "label": "编辑个人资料",
      "verb": "create",
      "intent": "edit_profile"
    },
    "rowActions": [
      {
        "label": "编辑",
        "verb": "edit",
        "intent": "edit_profile"
      }
    ],
    "special": "profile",
    "avatarPresets": [
      {
        "name": "青绿",
        "color": "#168b7d",
        "initial": "林"
      },
      {
        "name": "靛蓝",
        "color": "#5968b8",
        "initial": "林"
      },
      {
        "name": "暖橙",
        "color": "#b47d44",
        "initial": "林"
      },
      {
        "name": "石墨",
        "color": "#54636c",
        "initial": "林"
      }
    ],
    "sections": [
      {
        "title": "账户信息",
        "body": "个人资料不会改变登录身份或数据访问范围。",
        "items": [
          {
            "label": "登录账号",
            "value": "demo.operator"
          },
          {
            "label": "当前角色",
            "value": "安全负责人"
          },
          {
            "label": "账户来源",
            "value": "本地账号"
          }
        ]
      }
    ]
  },
  {
    "id": "admin/accounts",
    "section": "admin",
    "title": "账号安全",
    "subtitle": "查看登录状态、密码有效期与活动会话。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "账号"
      },
      {
        "key": "owner",
        "label": "使用者"
      },
      {
        "key": "status",
        "label": "登录状态"
      },
      {
        "key": "passwordState",
        "label": "密码状态"
      },
      {
        "key": "lastLogin",
        "label": "最近登录"
      }
    ],
    "rows": [
      {
        "id": "account-operator",
        "domain": "platform",
        "name": "demo.operator",
        "owner": "林青",
        "status": "已登录",
        "passwordState": "有效",
        "lastLogin": "2026-10-10 06:20 UTC",
        "expires": "2027-01-08",
        "sessions": "当前浏览器",
        "authFlow": "active",
        "current": true,
        "mfa": "已启用"
      },
      {
        "id": "account-new",
        "domain": "platform",
        "name": "demo.new",
        "owner": "顾言",
        "status": "待首次登录",
        "passwordState": "首次登录需更新",
        "lastLogin": "尚未登录",
        "expires": "完成首次登录后计算",
        "sessions": "无",
        "authFlow": "first_login",
        "mfa": "待绑定"
      },
      {
        "id": "account-expired",
        "domain": "platform",
        "name": "demo.expired",
        "owner": "周宁",
        "status": "待验证",
        "passwordState": "已过期",
        "lastLogin": "2026-10-08 09:12 UTC",
        "expires": "2026-10-09",
        "sessions": "已失效",
        "authFlow": "expired",
        "mfa": "已启用"
      }
    ],
    "fields": [
      {
        "key": "accountRef",
        "label": "选择账号",
        "type": "select",
        "options": [
          "demo.operator",
          "demo.new",
          "demo.expired"
        ],
        "required": true
      }
    ],
    "primary": {
      "label": "选择账号",
      "verb": "create",
      "intent": "select_account"
    },
    "rowActions": [
      {
        "label": "登录 / 退出",
        "verb": "toggle",
        "intent": "session_toggle",
        "confirm": true
      },
      {
        "label": "更新密码",
        "verb": "reset",
        "intent": "password_change",
        "confirm": true
      },
      {
        "label": "查看会话",
        "verb": "inspect",
        "intent": "sessions"
      }
    ],
    "special": "account-security",
    "help": "首次登录或密码过期时，先完成安全更新，再进入工作区。",
    "securityFlows": [
      {
        "state": "first_login",
        "title": "首次登录",
        "steps": [
          "选择账号",
          "更新密码",
          "完成身份验证",
          "进入工作区"
        ]
      },
      {
        "state": "expired",
        "title": "密码已过期",
        "steps": [
          "确认账号",
          "更新密码",
          "完成身份验证",
          "重新登录"
        ]
      },
      {
        "state": "active",
        "title": "账号登录",
        "steps": [
          "选择账号",
          "完成身份验证",
          "进入工作区"
        ]
      }
    ],
    "credentialInput": "disabled",
    "sections": [
      {
        "title": "登录保护",
        "body": "退出账号将结束当前会话。修改密码会使其他活动会话失效。",
        "items": [
          {
            "label": "密码有效期",
            "value": "90 天"
          },
          {
            "label": "会话有效期",
            "value": "30 分钟无操作后退出"
          },
          {
            "label": "二次验证",
            "value": "按账号策略要求"
          }
        ]
      }
    ]
  },
  {
    "id": "admin/mfa",
    "section": "admin",
    "title": "多因素认证",
    "subtitle": "为账号启用额外验证，并管理验证状态。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "账号"
      },
      {
        "key": "method",
        "label": "验证方式"
      },
      {
        "key": "status",
        "label": "认证状态"
      },
      {
        "key": "requirement",
        "label": "验证要求"
      },
      {
        "key": "lastVerified",
        "label": "最近验证"
      }
    ],
    "rows": [
      {
        "id": "mfa-operator",
        "domain": "platform",
        "name": "demo.operator",
        "method": "身份验证器",
        "status": "已启用",
        "requirement": "登录与敏感操作",
        "lastVerified": "2026-10-10 06:20 UTC",
        "boundAt": "2026-09-12 10:00 UTC",
        "verified": true,
        "enabled": true,
        "current": true
      },
      {
        "id": "mfa-new",
        "domain": "platform",
        "name": "demo.new",
        "method": "身份验证器",
        "status": "未绑定",
        "requirement": "首次登录时绑定",
        "lastVerified": "尚未验证",
        "boundAt": "未绑定",
        "verified": false,
        "enabled": false
      },
      {
        "id": "mfa-maintainer",
        "domain": "platform",
        "name": "demo.maintainer",
        "method": "身份验证器",
        "status": "已启用",
        "requirement": "登录与运维操作",
        "lastVerified": "2026-10-09 11:42 UTC",
        "boundAt": "2026-09-14 08:00 UTC",
        "verified": false,
        "enabled": true
      }
    ],
    "fields": [
      {
        "key": "accountRef",
        "label": "选择账号",
        "type": "select",
        "options": [
          "demo.operator",
          "demo.new",
          "demo.maintainer"
        ],
        "required": true
      }
    ],
    "primary": {
      "label": "管理认证方式",
      "verb": "create",
      "intent": "select_mfa_account"
    },
    "rowActions": [
      {
        "label": "启用 / 停用",
        "verb": "toggle",
        "intent": "mfa_toggle",
        "confirm": true
      },
      {
        "label": "验证身份",
        "verb": "test",
        "intent": "mfa_verify"
      },
      {
        "label": "重置绑定",
        "verb": "reset",
        "intent": "mfa_reset",
        "confirm": true
      }
    ],
    "special": "mfa",
    "credentialInput": "disabled",
    "help": "启用后，登录和敏感操作将要求额外验证。重置绑定会使该账号的现有验证失效。",
    "verificationSteps": [
      "确认账号",
      "关联身份验证器",
      "完成验证",
      "启用保护"
    ],
    "sections": [
      {
        "title": "保护范围",
        "body": "用户权限、资源授权与服务运维操作均需要近期的身份验证。",
        "items": [
          {
            "label": "验证有效窗口",
            "value": "5 分钟"
          },
          {
            "label": "重置后的状态",
            "value": "等待重新绑定"
          },
          {
            "label": "停用影响",
            "value": "账号失去额外验证保护"
          }
        ]
      }
    ]
  },
  {
    "id": "admin/users",
    "section": "admin",
    "title": "平台用户",
    "subtitle": "维护用户信息、角色分配与账号状态。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "用户"
      },
      {
        "key": "username",
        "label": "登录账号"
      },
      {
        "key": "role",
        "label": "角色"
      },
      {
        "key": "resourceGroup",
        "label": "资源组"
      },
      {
        "key": "status",
        "label": "状态"
      }
    ],
    "rows": [
      {
        "id": "user-operator",
        "domain": "platform",
        "name": "林青",
        "username": "demo.operator",
        "email": "demo.operator@example.test",
        "role": "安全负责人",
        "resourceGroup": "研发域、双域调查",
        "status": "启用",
        "lastLogin": "2026-10-10 06:20 UTC",
        "mfa": "已启用",
        "current": true,
        "protectedRole": true,
        "description": "安全事件调查与日常运营"
      },
      {
        "id": "user-viewer",
        "domain": "platform",
        "name": "周宁",
        "username": "demo.viewer",
        "email": "demo.viewer@example.test",
        "role": "只读调查员",
        "resourceGroup": "研发域",
        "status": "启用",
        "lastLogin": "2026-10-09 16:14 UTC",
        "mfa": "已启用",
        "description": "只读调查与证据核对"
      },
      {
        "id": "user-maintainer",
        "domain": "platform",
        "name": "顾言",
        "username": "demo.maintainer",
        "email": "demo.maintainer@example.test",
        "role": "运维管理员",
        "resourceGroup": "无域授权",
        "status": "启用",
        "lastLogin": "2026-10-09 11:42 UTC",
        "mfa": "已启用",
        "description": "维护平台服务与运行配置"
      },
      {
        "id": "user-platform",
        "domain": "platform",
        "name": "陈禾",
        "username": "demo.platform",
        "email": "demo.platform@example.test",
        "role": "平台管理员",
        "resourceGroup": "无域授权",
        "status": "启用",
        "lastLogin": "2026-10-09 09:10 UTC",
        "mfa": "已启用",
        "lastAdministrator": true,
        "description": "平台用户、角色与许可管理"
      }
    ],
    "fields": [
      {
        "key": "name",
        "label": "姓名",
        "type": "text",
        "required": true
      },
      {
        "key": "username",
        "label": "登录账号",
        "type": "text",
        "required": true
      },
      {
        "key": "email",
        "label": "联系邮箱",
        "type": "text"
      },
      {
        "key": "role",
        "label": "角色",
        "type": "select",
        "options": [
          "安全负责人",
          "只读调查员",
          "运维管理员",
          "平台管理员"
        ],
        "required": true
      },
      {
        "key": "resourceGroup",
        "label": "资源组",
        "type": "select",
        "options": [
          "无域授权",
          "研发域",
          "财务域",
          "双域调查"
        ],
        "required": true
      },
      {
        "key": "status",
        "label": "账号状态",
        "type": "select",
        "options": [
          "启用",
          "停用"
        ],
        "required": true
      },
      {
        "key": "description",
        "label": "备注",
        "type": "textarea"
      }
    ],
    "primary": {
      "label": "新增用户",
      "verb": "create"
    },
    "rowActions": [
      {
        "label": "编辑用户",
        "verb": "edit"
      },
      {
        "label": "分配角色",
        "verb": "edit",
        "intent": "assign_roles",
        "confirm": true
      },
      {
        "label": "重置密码",
        "verb": "reset",
        "intent": "admin_password_reset",
        "confirm": true
      },
      {
        "label": "启用 / 停用",
        "verb": "toggle",
        "intent": "user_toggle",
        "confirm": true
      },
      {
        "label": "删除用户",
        "verb": "delete",
        "intent": "delete_user",
        "confirm": true
      }
    ],
    "special": "users",
    "bulkActions": [
      {
        "label": "批量分配角色",
        "verb": "edit",
        "intent": "assign_roles",
        "confirm": true
      },
      {
        "label": "停用所选用户",
        "verb": "toggle",
        "intent": "disable_users",
        "confirm": true
      }
    ],
    "guards": [
      "no_self_elevation",
      "no_self_disable",
      "preserve_last_administrator",
      "fresh_verification"
    ],
    "sections": [
      {
        "title": "角色变更",
        "body": "角色调整后，受影响用户需要重新登录。功能权限与资源范围分别生效。"
      }
    ]
  },
  {
    "id": "admin/roles",
    "section": "admin",
    "title": "角色与功能权限",
    "subtitle": "配置页面访问和操作权限，查看角色成员。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "角色名称"
      },
      {
        "key": "memberCount",
        "label": "用户数"
      },
      {
        "key": "resourceGroup",
        "label": "资源组"
      },
      {
        "key": "permissionSummary",
        "label": "关键权限"
      },
      {
        "key": "status",
        "label": "状态"
      }
    ],
    "rows": [
      {
        "id": "role-operator",
        "domain": "platform",
        "name": "安全负责人",
        "memberCount": 1,
        "resourceGroup": "研发域、双域调查",
        "permissionSummary": "完整运营、响应与平台管理",
        "status": "启用",
        "description": "负责平台安全运营、调查响应与管理协作",
        "assetView": true,
        "assetExport": true,
        "detectionView": true,
        "detectionRun": true,
        "ruleManage": true,
        "investigationView": true,
        "responseView": true,
        "responseExecute": true,
        "reportView": true,
        "reportExport": true,
        "taskManage": true,
        "userManage": true,
        "roleManage": true,
        "resourceManage": true,
        "auditView": true,
        "auditExport": true,
        "systemView": true,
        "systemManage": true,
        "serviceOperate": true,
        "members": [
          "user-operator"
        ],
        "protectedCurrent": true
      },
      {
        "id": "role-viewer",
        "domain": "platform",
        "name": "只读调查员",
        "memberCount": 1,
        "resourceGroup": "研发域",
        "permissionSummary": "只读调查",
        "status": "启用",
        "description": "查看已授权范围内的资产和调查",
        "assetView": true,
        "assetExport": false,
        "detectionView": true,
        "detectionRun": false,
        "ruleManage": false,
        "investigationView": true,
        "responseView": true,
        "responseExecute": false,
        "reportView": true,
        "reportExport": false,
        "taskManage": false,
        "userManage": false,
        "roleManage": false,
        "resourceManage": false,
        "auditView": false,
        "auditExport": false,
        "systemView": false,
        "systemManage": false,
        "serviceOperate": false,
        "members": [
          "user-viewer"
        ]
      },
      {
        "id": "role-maintainer",
        "domain": "platform",
        "name": "运维管理员",
        "memberCount": 1,
        "resourceGroup": "无域授权",
        "permissionSummary": "平台监控、服务运维",
        "status": "启用",
        "description": "仅平台运行与诊断，无 AD 数据权限",
        "assetView": false,
        "assetExport": false,
        "detectionView": false,
        "detectionRun": false,
        "ruleManage": false,
        "investigationView": false,
        "responseView": false,
        "responseExecute": false,
        "reportView": false,
        "reportExport": false,
        "taskManage": false,
        "userManage": false,
        "roleManage": false,
        "resourceManage": false,
        "auditView": true,
        "auditExport": false,
        "systemView": true,
        "systemManage": true,
        "serviceOperate": true,
        "members": [
          "user-maintainer"
        ]
      },
      {
        "id": "role-platform",
        "domain": "platform",
        "name": "平台管理员",
        "memberCount": 1,
        "resourceGroup": "无域授权",
        "permissionSummary": "身份、授权、配置",
        "status": "启用",
        "description": "平台管理与授权维护",
        "assetView": false,
        "assetExport": false,
        "detectionView": false,
        "detectionRun": false,
        "ruleManage": false,
        "investigationView": false,
        "responseView": false,
        "responseExecute": false,
        "reportView": false,
        "reportExport": false,
        "taskManage": false,
        "userManage": true,
        "roleManage": true,
        "resourceManage": true,
        "auditView": true,
        "auditExport": true,
        "systemView": true,
        "systemManage": true,
        "serviceOperate": false,
        "members": [
          "user-platform"
        ],
        "lastAdministrator": true
      }
    ],
    "fields": [
      {
        "key": "name",
        "label": "角色名称",
        "type": "text",
        "required": true
      },
      {
        "key": "description",
        "label": "角色说明",
        "type": "textarea"
      },
      {
        "key": "assetView",
        "label": "查看资产",
        "type": "checkbox"
      },
      {
        "key": "assetExport",
        "label": "导出资产",
        "type": "checkbox"
      },
      {
        "key": "detectionView",
        "label": "查看检测",
        "type": "checkbox"
      },
      {
        "key": "detectionRun",
        "label": "运行检测",
        "type": "checkbox"
      },
      {
        "key": "ruleManage",
        "label": "管理规则",
        "type": "checkbox"
      },
      {
        "key": "investigationView",
        "label": "查看调查",
        "type": "checkbox"
      },
      {
        "key": "responseView",
        "label": "查看处置",
        "type": "checkbox"
      },
      {
        "key": "responseExecute",
        "label": "执行处置",
        "type": "checkbox"
      },
      {
        "key": "reportView",
        "label": "查看报告",
        "type": "checkbox"
      },
      {
        "key": "reportExport",
        "label": "导出报告",
        "type": "checkbox"
      },
      {
        "key": "taskManage",
        "label": "管理任务",
        "type": "checkbox"
      },
      {
        "key": "userManage",
        "label": "管理用户",
        "type": "checkbox"
      },
      {
        "key": "roleManage",
        "label": "管理角色",
        "type": "checkbox"
      },
      {
        "key": "resourceManage",
        "label": "管理资源组",
        "type": "checkbox"
      },
      {
        "key": "auditView",
        "label": "查看平台审计",
        "type": "checkbox"
      },
      {
        "key": "auditExport",
        "label": "导出平台审计",
        "type": "checkbox"
      },
      {
        "key": "systemView",
        "label": "查看平台状态",
        "type": "checkbox"
      },
      {
        "key": "systemManage",
        "label": "修改平台配置",
        "type": "checkbox"
      },
      {
        "key": "serviceOperate",
        "label": "操作平台服务",
        "type": "checkbox"
      }
    ],
    "primary": {
      "label": "新增角色",
      "verb": "create"
    },
    "rowActions": [
      {
        "label": "编辑权限",
        "verb": "edit",
        "intent": "edit_permissions",
        "confirm": true
      },
      {
        "label": "查看成员",
        "verb": "inspect",
        "intent": "role_members"
      },
      {
        "label": "删除角色",
        "verb": "delete",
        "intent": "delete_role",
        "confirm": true
      }
    ],
    "special": "permissions",
    "permissionMatrix": [
      {
        "title": "目录资产",
        "items": [
          {
            "key": "assetView",
            "label": "查看资产"
          },
          {
            "key": "assetExport",
            "label": "导出资产"
          }
        ]
      },
      {
        "title": "检测与调查",
        "items": [
          {
            "key": "detectionView",
            "label": "查看检测"
          },
          {
            "key": "detectionRun",
            "label": "运行检测"
          },
          {
            "key": "ruleManage",
            "label": "管理规则"
          },
          {
            "key": "investigationView",
            "label": "查看调查"
          }
        ]
      },
      {
        "title": "响应处置",
        "items": [
          {
            "key": "responseView",
            "label": "查看处置"
          },
          {
            "key": "responseExecute",
            "label": "执行处置"
          }
        ]
      },
      {
        "title": "报告与任务",
        "items": [
          {
            "key": "reportView",
            "label": "查看报告"
          },
          {
            "key": "reportExport",
            "label": "导出报告"
          },
          {
            "key": "taskManage",
            "label": "管理任务"
          }
        ]
      },
      {
        "title": "平台管理",
        "items": [
          {
            "key": "userManage",
            "label": "管理用户"
          },
          {
            "key": "roleManage",
            "label": "管理角色"
          },
          {
            "key": "resourceManage",
            "label": "管理资源组"
          },
          {
            "key": "auditView",
            "label": "查看平台审计"
          },
          {
            "key": "auditExport",
            "label": "导出平台审计"
          }
        ]
      },
      {
        "title": "系统运维",
        "items": [
          {
            "key": "systemView",
            "label": "查看平台状态"
          },
          {
            "key": "systemManage",
            "label": "修改平台配置"
          },
          {
            "key": "serviceOperate",
            "label": "操作平台服务"
          }
        ]
      }
    ],
    "guards": [
      "no_self_elevation",
      "preserve_last_administrator",
      "fresh_verification"
    ],
    "help": "未授予的权限默认关闭。拥有页面访问权限，不代表拥有该页面中的操作权限。",
    "sections": [
      {
        "title": "资源范围",
        "body": "角色的功能权限决定可以做什么；关联的资源组决定可以访问哪些域。"
      }
    ]
  },
  {
    "id": "admin/resources",
    "section": "admin",
    "title": "资源组与租户",
    "subtitle": "为角色明确授权域范围，管理租户设置。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "资源组"
      },
      {
        "key": "tenant",
        "label": "所属租户"
      },
      {
        "key": "domainSet",
        "label": "授权域"
      },
      {
        "key": "roles",
        "label": "关联角色"
      },
      {
        "key": "status",
        "label": "状态"
      }
    ],
    "rows": [
      {
        "id": "resource-alpha",
        "domain": "platform",
        "name": "研发域",
        "tenant": "安全研究室",
        "domainSet": "alpha.example.test",
        "roles": "安全负责人、只读调查员",
        "status": "启用",
        "description": "研发目录的调查与检测范围",
        "domains": [
          "alpha"
        ],
        "roleIds": [
          "role-operator",
          "role-viewer"
        ],
        "version": 3
      },
      {
        "id": "resource-beta",
        "domain": "platform",
        "name": "财务域",
        "tenant": "安全研究室",
        "domainSet": "beta.example.test",
        "roles": "未关联",
        "status": "未分配",
        "description": "财务目录的独立授权范围",
        "domains": [
          "beta"
        ],
        "roleIds": [],
        "version": 1
      },
      {
        "id": "resource-both",
        "domain": "platform",
        "name": "双域调查",
        "tenant": "安全研究室",
        "domainSet": "alpha.example.test、beta.example.test",
        "roles": "安全负责人",
        "status": "启用",
        "description": "跨域事件调查时按需分配",
        "domains": [
          "alpha",
          "beta"
        ],
        "roleIds": [
          "role-operator"
        ],
        "version": 1
      }
    ],
    "fields": [
      {
        "key": "name",
        "label": "资源组名称",
        "type": "text",
        "required": true
      },
      {
        "key": "tenant",
        "label": "所属租户",
        "type": "select",
        "options": [
          "安全研究室"
        ],
        "required": true
      },
      {
        "key": "domainSet",
        "label": "授权域",
        "type": "select",
        "options": [
          "alpha.example.test",
          "beta.example.test",
          "alpha.example.test、beta.example.test"
        ],
        "required": true
      },
      {
        "key": "roles",
        "label": "关联角色",
        "type": "select",
        "options": [
          "未关联",
          "安全负责人",
          "只读调查员",
          "安全负责人、只读调查员"
        ],
        "required": true
      },
      {
        "key": "description",
        "label": "说明",
        "type": "textarea"
      }
    ],
    "primary": {
      "label": "新增资源组",
      "verb": "create"
    },
    "rowActions": [
      {
        "label": "编辑资源组",
        "verb": "edit",
        "intent": "edit_resource",
        "confirm": true
      },
      {
        "label": "关联角色",
        "verb": "edit",
        "intent": "associate_roles",
        "confirm": true
      },
      {
        "label": "删除资源组",
        "verb": "delete",
        "intent": "delete_resource",
        "confirm": true
      }
    ],
    "special": "resources",
    "toolbarActions": [
      {
        "label": "租户设置",
        "verb": "edit",
        "intent": "tenant_settings",
        "confirm": true
      }
    ],
    "tenantSettings": {
      "id": "tenant-lab",
      "name": "安全研究室",
      "slug": "security-lab",
      "timezone": "Asia/Shanghai",
      "defaultScope": "仅显式授权",
      "sessionTimeout": 30,
      "fieldKeys": [
        "tenantName",
        "timezone",
        "sessionTimeout"
      ]
    },
    "tenantFields": [
      {
        "key": "tenantName",
        "label": "租户名称",
        "type": "text",
        "required": true
      },
      {
        "key": "timezone",
        "label": "默认时区",
        "type": "select",
        "options": [
          "Asia/Shanghai",
          "UTC"
        ]
      },
      {
        "key": "sessionTimeout",
        "label": "会话超时（分钟）",
        "type": "number",
        "min": 5,
        "max": 120
      }
    ],
    "guards": [
      "no_self_elevation",
      "fresh_verification"
    ],
    "help": "平台管理员不会自动获得任何 AD 域的数据权限。移除资源组关联后，相关数据访问会立即失效。"
  },
  {
    "id": "admin/audit",
    "section": "admin",
    "title": "平台操作审计",
    "subtitle": "按操作人、类型和时间查找平台管理记录。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "操作"
      },
      {
        "key": "actor",
        "label": "操作人"
      },
      {
        "key": "auditType",
        "label": "审计类型"
      },
      {
        "key": "target",
        "label": "操作对象"
      },
      {
        "key": "result",
        "label": "结果"
      },
      {
        "key": "time",
        "label": "发生时间"
      }
    ],
    "rows": [
      {
        "id": "audit-3101",
        "domain": "platform",
        "name": "调整存储告警阈值",
        "actor": "demo.maintainer",
        "auditType": "系统设置",
        "target": "存储管理",
        "result": "成功",
        "time": "2026-10-10 06:28 UTC",
        "status": "可见",
        "archived": false,
        "requestId": "req-demo-3101",
        "changes": "告警阈值：85% → 88%",
        "origin": "管理控制台",
        "visibility": "platform"
      },
      {
        "id": "audit-3102",
        "domain": "platform",
        "name": "分配用户角色",
        "actor": "demo.platform",
        "auditType": "身份与权限",
        "target": "demo.viewer",
        "result": "成功",
        "time": "2026-10-10 06:12 UTC",
        "status": "可见",
        "archived": false,
        "requestId": "req-demo-3102",
        "changes": "角色：只读调查员",
        "origin": "管理控制台",
        "visibility": "platform"
      },
      {
        "id": "audit-3103",
        "domain": "platform",
        "name": "服务重启请求",
        "actor": "demo.maintainer",
        "auditType": "服务运维",
        "target": "任务服务",
        "result": "已拒绝",
        "time": "2026-10-09 18:40 UTC",
        "status": "可见",
        "archived": false,
        "requestId": "req-demo-3103",
        "changes": "身份验证已过期，服务状态未改变",
        "origin": "运维控制台",
        "visibility": "platform"
      },
      {
        "id": "audit-3099",
        "domain": "platform",
        "name": "更新平台显示名称",
        "actor": "demo.platform",
        "auditType": "系统设置",
        "target": "系统标识",
        "result": "成功",
        "time": "2026-10-08 09:10 UTC",
        "status": "已归档",
        "archived": true,
        "requestId": "req-demo-3099",
        "changes": "显示名称：ADTR 安全平台",
        "origin": "管理控制台",
        "visibility": "platform"
      }
    ],
    "fields": [
      {
        "key": "auditType",
        "label": "审计类型",
        "type": "select",
        "options": [
          "全部类型",
          "身份与权限",
          "系统设置",
          "服务运维",
          "集成授权"
        ]
      },
      {
        "key": "actor",
        "label": "操作人",
        "type": "select",
        "options": [
          "全部操作人",
          "demo.operator",
          "demo.platform",
          "demo.maintainer"
        ]
      },
      {
        "key": "startTime",
        "label": "开始时间",
        "type": "text"
      },
      {
        "key": "endTime",
        "label": "结束时间",
        "type": "text"
      },
      {
        "key": "visibility",
        "label": "记录状态",
        "type": "select",
        "options": [
          "可见记录",
          "已归档记录",
          "全部记录"
        ]
      }
    ],
    "primary": {
      "label": "导出审计",
      "verb": "create",
      "intent": "audit_export"
    },
    "rowActions": [
      {
        "label": "查看详情",
        "verb": "inspect"
      },
      {
        "label": "归档 / 还原",
        "verb": "toggle",
        "intent": "audit_archive",
        "confirm": true
      }
    ],
    "special": "audit",
    "bulkActions": [
      {
        "label": "导出所选记录",
        "verb": "download",
        "intent": "audit_export"
      },
      {
        "label": "归档 / 还原所选记录",
        "verb": "toggle",
        "intent": "audit_archive",
        "confirm": true
      }
    ],
    "filterFields": [
      "auditType",
      "actor",
      "result",
      "status",
      "startTime",
      "endTime"
    ],
    "sections": [
      {
        "title": "操作追踪",
        "body": "审计记录保留操作人、对象、时间和结果。归档记录可以在“已归档”中查找并还原。"
      }
    ]
  },
  {
    "id": "admin/integrations",
    "section": "admin",
    "title": "第三方访问授权",
    "subtitle": "管理外部应用的访问范围、有效期和授权状态。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "应用"
      },
      {
        "key": "provider",
        "label": "接入方式"
      },
      {
        "key": "permission",
        "label": "授权范围"
      },
      {
        "key": "credentialRef",
        "label": "凭据引用"
      },
      {
        "key": "expires",
        "label": "有效期"
      },
      {
        "key": "status",
        "label": "状态"
      }
    ],
    "rows": [
      {
        "id": "integration-soc",
        "domain": "platform",
        "name": "SOC 事件同步",
        "provider": "API 授权",
        "permission": "平台健康：只读",
        "credentialRef": "凭据库 / soc-health-reader",
        "expires": "2026-12-31",
        "status": "已授权",
        "enabled": true,
        "owner": "安全运营中心",
        "callback": "无回调",
        "lastUsed": "2026-10-10 06:30 UTC",
        "authorizationRef": "grant-demo-soc-01"
      },
      {
        "id": "integration-ticket",
        "domain": "platform",
        "name": "运维工单",
        "provider": "OAuth 2.0",
        "permission": "平台任务：只读",
        "credentialRef": "凭据库 / ticket-reader",
        "expires": "2026-11-30",
        "status": "待授权",
        "enabled": false,
        "owner": "基础设施团队",
        "callback": "https://tickets.example.test/oauth/callback",
        "lastUsed": "尚未访问",
        "authorizationRef": "grant-demo-ticket-01"
      },
      {
        "id": "integration-sso",
        "domain": "platform",
        "name": "统一身份入口",
        "provider": "OIDC",
        "permission": "身份声明：只读",
        "credentialRef": "凭据库 / sso-client",
        "expires": "2026-09-30",
        "status": "已过期",
        "enabled": false,
        "owner": "身份管理团队",
        "callback": "https://adtr.example.test/auth/callback",
        "lastUsed": "2026-09-29 15:08 UTC",
        "authorizationRef": "grant-demo-sso-01"
      }
    ],
    "fields": [
      {
        "key": "name",
        "label": "应用名称",
        "type": "text",
        "required": true
      },
      {
        "key": "provider",
        "label": "接入方式",
        "type": "select",
        "options": [
          "API 授权",
          "OAuth 2.0",
          "OIDC"
        ],
        "required": true
      },
      {
        "key": "permission",
        "label": "授权范围",
        "type": "select",
        "options": [
          "平台健康：只读",
          "平台任务：只读",
          "身份声明：只读"
        ],
        "required": true
      },
      {
        "key": "credentialRef",
        "label": "凭据引用",
        "type": "select",
        "options": [
          "凭据库 / soc-health-reader",
          "凭据库 / ticket-reader",
          "凭据库 / sso-client"
        ],
        "required": true
      },
      {
        "key": "expires",
        "label": "授权到期日",
        "type": "text",
        "required": true
      },
      {
        "key": "owner",
        "label": "责任团队",
        "type": "select",
        "options": [
          "安全运营中心",
          "基础设施团队",
          "身份管理团队"
        ]
      }
    ],
    "primary": {
      "label": "登记应用",
      "verb": "create"
    },
    "rowActions": [
      {
        "label": "编辑授权",
        "verb": "edit"
      },
      {
        "label": "查看授权流程",
        "verb": "inspect",
        "intent": "authorization_preview"
      },
      {
        "label": "启用 / 撤销",
        "verb": "toggle",
        "intent": "integration_toggle",
        "confirm": true
      },
      {
        "label": "检查授权",
        "verb": "test",
        "intent": "integration_test"
      }
    ],
    "special": "integrations",
    "credentialInput": "disabled",
    "authorizationSteps": [
      "确认应用与回调地址",
      "核对请求权限",
      "确认授权有效期",
      "返回授权结果"
    ],
    "help": "应用只能使用已登记的凭据引用。撤销授权后，该应用将不能继续访问平台。"
  },
  {
    "id": "admin/system",
    "section": "admin",
    "title": "系统信息与监控",
    "subtitle": "查看节点资源、存储容量与关键依赖的健康状态。",
    "scope": "platform",
    "kind": "dashboard",
    "columns": [
      {
        "key": "name",
        "label": "节点 / 依赖"
      },
      {
        "key": "type",
        "label": "组件类型"
      },
      {
        "key": "status",
        "label": "状态"
      },
      {
        "key": "cpu",
        "label": "CPU"
      },
      {
        "key": "memory",
        "label": "内存"
      },
      {
        "key": "storage",
        "label": "存储"
      },
      {
        "key": "observedAt",
        "label": "采样时间"
      }
    ],
    "rows": [
      {
        "id": "system-api",
        "domain": "platform",
        "name": "控制节点",
        "type": "API 节点",
        "status": "健康",
        "cpu": "24%",
        "memory": "3.8 / 8 GB",
        "storage": "132 / 200 GB",
        "observedAt": "2026-10-10 06:35 UTC",
        "address": "api-01.example.test",
        "version": "ADTR 1.4.2",
        "uptime": "12 天 4 小时",
        "lastError": "无",
        "os": "Linux x86_64"
      },
      {
        "id": "system-worker",
        "domain": "platform",
        "name": "任务节点",
        "type": "Worker 节点",
        "status": "健康",
        "cpu": "42%",
        "memory": "5.2 / 16 GB",
        "storage": "42 / 100 GB",
        "observedAt": "2026-10-10 06:35 UTC",
        "address": "worker-01.example.test",
        "version": "ADTR 1.4.2",
        "uptime": "12 天 4 小时",
        "lastError": "无",
        "queueDepth": 3,
        "os": "Linux x86_64"
      },
      {
        "id": "system-database",
        "domain": "platform",
        "name": "平台数据库",
        "type": "PostgreSQL",
        "status": "健康",
        "cpu": "18%",
        "memory": "2.1 / 8 GB",
        "storage": "96 / 200 GB",
        "observedAt": "2026-10-10 06:35 UTC",
        "address": "db-01.example.test",
        "version": "PostgreSQL 17",
        "uptime": "18 天 9 小时",
        "lastError": "无",
        "connections": "12 / 100"
      },
      {
        "id": "system-cache",
        "domain": "platform",
        "name": "查询缓存",
        "type": "缓存依赖",
        "status": "未配置",
        "cpu": "—",
        "memory": "—",
        "storage": "—",
        "observedAt": "未观测",
        "address": "未配置",
        "version": "—",
        "uptime": "—",
        "lastError": "尚未启用此依赖"
      }
    ],
    "fields": [
      {
        "key": "storageWarning",
        "label": "容量告警阈值（%）",
        "type": "number",
        "required": true,
        "min": 85,
        "max": 90
      },
      {
        "key": "auditRetention",
        "label": "审计保留期（天）",
        "type": "number",
        "required": true,
        "min": 30,
        "max": 3650
      },
      {
        "key": "diagnosticRetention",
        "label": "诊断包保留期（天）",
        "type": "number",
        "required": true,
        "min": 1,
        "max": 90
      },
      {
        "key": "notifyWarning",
        "label": "容量超限时通知管理员",
        "type": "checkbox"
      }
    ],
    "primary": {
      "label": "存储管理",
      "verb": "create",
      "intent": "storage_settings"
    },
    "rowActions": [
      {
        "label": "查看资源",
        "verb": "inspect"
      },
      {
        "label": "刷新状态",
        "verb": "test",
        "intent": "refresh_health"
      }
    ],
    "special": "system",
    "settings": {
      "storageWarning": 88,
      "auditRetention": 365,
      "diagnosticRetention": 14,
      "notifyWarning": true
    },
    "stats": [
      {
        "label": "CPU 使用率",
        "value": "24%",
        "detail": "控制节点 · 4 核"
      },
      {
        "label": "内存使用率",
        "value": "48%",
        "detail": "3.8 GB / 8 GB"
      },
      {
        "label": "存储使用率",
        "value": "66%",
        "detail": "132 GB / 200 GB"
      },
      {
        "label": "核心服务",
        "value": "3 / 3",
        "detail": "最近采样 06:35 UTC"
      }
    ],
    "sections": [
      {
        "title": "平台信息",
        "body": "ADTR 安全平台",
        "items": [
          {
            "label": "版本",
            "value": "1.4.2"
          },
          {
            "label": "部署标识",
            "value": "demo-appliance-01"
          },
          {
            "label": "运行模式",
            "value": "API 与任务节点分离"
          },
          {
            "label": "平台时间",
            "value": "2026-10-10 06:35 UTC"
          }
        ]
      },
      {
        "title": "存储策略",
        "body": "容量告警不会自动删除审计或业务数据。",
        "items": [
          {
            "label": "容量告警",
            "value": "88%"
          },
          {
            "label": "审计保留期",
            "value": "365 天"
          },
          {
            "label": "诊断包保留期",
            "value": "14 天"
          }
        ]
      }
    ]
  },
  {
    "id": "admin/license",
    "section": "admin",
    "title": "许可与激活",
    "subtitle": "查看授权范围、设备绑定与许可有效期。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "许可"
      },
      {
        "key": "licenseType",
        "label": "版本"
      },
      {
        "key": "deviceId",
        "label": "绑定设备"
      },
      {
        "key": "quota",
        "label": "授权额度"
      },
      {
        "key": "expires",
        "label": "到期日"
      },
      {
        "key": "status",
        "label": "状态"
      }
    ],
    "rows": [
      {
        "id": "license-current",
        "domain": "platform",
        "name": "当前企业许可",
        "licenseType": "企业版",
        "deviceId": "demo-appliance-01",
        "quota": "5 个域 / 50,000 个对象",
        "expires": "2026-12-31",
        "status": "已激活",
        "packageRef": "enterprise-2026.lic",
        "starts": "2026-01-01",
        "domainsAllowed": 5,
        "objectsAllowed": 50000,
        "modules": "接入、资产、检测、调查、响应、报告",
        "validation": "通过",
        "deviceMatched": true,
        "active": true,
        "packageReady": true
      },
      {
        "id": "license-renewal",
        "domain": "platform",
        "name": "企业续期许可",
        "licenseType": "企业版",
        "deviceId": "demo-appliance-01",
        "quota": "10 个域 / 100,000 个对象",
        "expires": "2027-12-31",
        "status": "待激活",
        "packageRef": "enterprise-renewal-2027.lic",
        "starts": "2026-10-10",
        "domainsAllowed": 10,
        "objectsAllowed": 100000,
        "modules": "接入、资产、检测、调查、响应、报告",
        "validation": "待校验",
        "deviceMatched": true,
        "active": false,
        "packageReady": true
      },
      {
        "id": "license-mismatch",
        "domain": "platform",
        "name": "待核对许可",
        "licenseType": "企业版",
        "deviceId": "demo-appliance-02",
        "quota": "3 个域 / 20,000 个对象",
        "expires": "2026-12-31",
        "status": "校验失败",
        "packageRef": "enterprise-other-device.lic",
        "starts": "2026-01-01",
        "domainsAllowed": 3,
        "objectsAllowed": 20000,
        "modules": "接入、资产、检测、调查、报告",
        "validation": "设备标识不匹配",
        "deviceMatched": false,
        "active": false,
        "packageReady": true
      }
    ],
    "fields": [
      {
        "key": "packageRef",
        "label": "选择许可文件",
        "type": "select",
        "options": [
          "enterprise-2026.lic",
          "enterprise-renewal-2027.lic",
          "enterprise-other-device.lic"
        ],
        "required": true
      }
    ],
    "primary": {
      "label": "更新许可",
      "verb": "create",
      "intent": "license_import"
    },
    "rowActions": [
      {
        "label": "授权预览",
        "verb": "inspect",
        "intent": "license_preview"
      },
      {
        "label": "校验许可",
        "verb": "test",
        "intent": "license_validate"
      },
      {
        "label": "激活许可",
        "verb": "activate",
        "intent": "license_activate",
        "confirm": true
      }
    ],
    "special": "license",
    "guards": [
      "device_match",
      "valid_date",
      "quota_sufficient",
      "fresh_verification"
    ],
    "currentDevice": "demo-appliance-01",
    "currentUsage": {
      "domains": 2,
      "objects": 18426
    },
    "stats": [
      {
        "label": "当前许可",
        "value": "企业版",
        "detail": "有效至 2026-12-31"
      },
      {
        "label": "已授权域",
        "value": "2 / 5",
        "detail": "已使用 / 可用额度"
      },
      {
        "label": "目录对象",
        "value": "18,426",
        "detail": "授权上限 50,000"
      },
      {
        "label": "设备绑定",
        "value": "已匹配",
        "detail": "demo-appliance-01"
      }
    ],
    "help": "激活前请核对设备标识、有效期、功能范围与授权额度。校验失败时，当前许可保持不变。"
  },
  {
    "id": "admin/settings",
    "section": "admin",
    "title": "系统设置",
    "subtitle": "管理平台标识、时间同步与管理策略。",
    "scope": "platform",
    "kind": "settings",
    "columns": [
      {
        "key": "name",
        "label": "设置分组"
      },
      {
        "key": "summary",
        "label": "当前设置"
      },
      {
        "key": "updated",
        "label": "最近更新"
      }
    ],
    "rows": [
      {
        "id": "settings-brand",
        "domain": "platform",
        "name": "系统标识",
        "summary": "ADTR 安全平台 · 标准图标",
        "systemName": "ADTR 安全平台",
        "logoPreset": "标准图标",
        "updated": "2026-10-08 09:10 UTC",
        "fieldKeys": [
          "systemName",
          "logoPreset"
        ]
      },
      {
        "id": "settings-time",
        "domain": "platform",
        "name": "系统时间",
        "summary": "Asia/Shanghai · 自动同步",
        "timezone": "Asia/Shanghai",
        "ntpEnabled": true,
        "ntpSource": "time-a.example.test",
        "manualTime": "2026-10-10 14:35",
        "lastSynced": "2026-10-10 06:34 UTC",
        "offset": "12 ms",
        "updated": "2026-10-10 06:35 UTC",
        "fieldKeys": [
          "timezone",
          "ntpEnabled",
          "ntpSource",
          "manualTime"
        ]
      },
      {
        "id": "settings-management",
        "domain": "platform",
        "name": "管理策略",
        "summary": "30 分钟会话 · 常规运行",
        "sessionTimeout": 30,
        "loginNotice": "请使用个人账号访问平台。",
        "maintenanceMode": false,
        "updated": "2026-10-09 08:20 UTC",
        "fieldKeys": [
          "sessionTimeout",
          "loginNotice",
          "maintenanceMode"
        ]
      }
    ],
    "fields": [
      {
        "key": "systemName",
        "label": "平台显示名称",
        "type": "text",
        "required": true
      },
      {
        "key": "logoPreset",
        "label": "系统标识",
        "type": "select",
        "options": [
          "标准图标",
          "深色图标",
          "简洁图标"
        ]
      },
      {
        "key": "timezone",
        "label": "时区",
        "type": "select",
        "options": [
          "Asia/Shanghai",
          "UTC",
          "Asia/Singapore"
        ],
        "required": true
      },
      {
        "key": "ntpEnabled",
        "label": "自动同步时间",
        "type": "checkbox"
      },
      {
        "key": "ntpSource",
        "label": "时间源",
        "type": "select",
        "options": [
          "time-a.example.test",
          "time-b.example.test"
        ]
      },
      {
        "key": "manualTime",
        "label": "手动设置时间",
        "type": "text"
      },
      {
        "key": "sessionTimeout",
        "label": "会话超时（分钟）",
        "type": "number",
        "required": true,
        "min": 5,
        "max": 120
      },
      {
        "key": "loginNotice",
        "label": "登录提示",
        "type": "textarea"
      },
      {
        "key": "maintenanceMode",
        "label": "维护模式",
        "type": "checkbox"
      }
    ],
    "primary": {
      "label": "编辑设置",
      "verb": "create",
      "intent": "edit_settings"
    },
    "rowActions": [
      {
        "label": "编辑设置",
        "verb": "edit",
        "intent": "edit_settings"
      },
      {
        "label": "立即同步",
        "verb": "test",
        "intent": "sync_time",
        "appliesTo": [
          "settings-time"
        ]
      }
    ],
    "special": "settings",
    "guards": [
      "fresh_verification"
    ],
    "sections": [
      {
        "title": "时间同步",
        "body": "系统时间影响事件顺序、任务计划与认证有效期。调整前请核对当前时区及时间源。"
      }
    ]
  },
  {
    "id": "admin/services",
    "section": "admin",
    "title": "服务与运维",
    "subtitle": "维护服务运行状态，并跟踪恢复进度。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "服务"
      },
      {
        "key": "status",
        "label": "运行状态"
      },
      {
        "key": "node",
        "label": "所在节点"
      },
      {
        "key": "affectedScope",
        "label": "影响范围"
      },
      {
        "key": "lastChanged",
        "label": "最近变更"
      }
    ],
    "rows": [
      {
        "id": "service-api",
        "domain": "platform",
        "name": "平台 API",
        "status": "运行中",
        "node": "api-01",
        "affectedScope": "所有用户的页面读取与操作请求",
        "lastChanged": "2026-10-09 01:30 UTC",
        "version": "1.4.2",
        "processes": 2,
        "activeUsers": 4,
        "dependencies": "平台数据库",
        "recovery": "已在线",
        "canStart": true,
        "canStop": true,
        "canRestart": true,
        "impact": "停止期间平台页面无法读取或提交请求。已有后台任务仍由任务服务处理。"
      },
      {
        "id": "service-worker",
        "domain": "platform",
        "name": "任务服务",
        "status": "运行中",
        "node": "worker-01",
        "affectedScope": "3 个排队任务；1 个运行中任务",
        "lastChanged": "2026-10-09 01:30 UTC",
        "version": "1.4.2",
        "processes": 2,
        "queueDepth": 3,
        "runningTasks": 1,
        "dependencies": "平台数据库、平台 API",
        "recovery": "已在线",
        "canStart": true,
        "canStop": true,
        "canRestart": true,
        "impact": "停止后不再领取新任务。运行中的任务需等待停止确认，不能视为已完成或已取消。"
      },
      {
        "id": "service-log",
        "domain": "platform",
        "name": "诊断归档服务",
        "status": "已停止",
        "node": "worker-01",
        "affectedScope": "诊断包生成与下载准备",
        "lastChanged": "2026-10-10 04:12 UTC",
        "version": "1.4.2",
        "processes": 0,
        "dependencies": "归档存储",
        "recovery": "等待启动",
        "canStart": true,
        "canStop": true,
        "canRestart": true,
        "impact": "停止期间无法生成新的诊断包，已有诊断文件保持不变。"
      },
      {
        "id": "service-platform",
        "domain": "platform",
        "name": "整个平台",
        "status": "运行中",
        "node": "全部平台节点",
        "affectedScope": "4 个在线用户；所有平台服务",
        "lastChanged": "2026-10-09 01:30 UTC",
        "version": "1.4.2",
        "processes": 4,
        "activeUsers": 4,
        "dependencies": "平台数据库、归档存储",
        "recovery": "已在线",
        "canStart": false,
        "canStop": false,
        "canRestart": true,
        "impact": "重启将暂时中断全部用户会话，并暂停任务领取。恢复在线后需要重新登录。"
      }
    ],
    "fields": [],
    "primary": {
      "label": "进入运维",
      "verb": "create",
      "intent": "operations_login"
    },
    "rowActions": [
      {
        "label": "启动 / 停止",
        "verb": "toggle",
        "intent": "service_toggle",
        "confirm": true,
        "excludeIds": [
          "service-platform"
        ]
      },
      {
        "label": "重启",
        "verb": "restart",
        "intent": "service_restart",
        "confirm": true
      },
      {
        "label": "查看恢复状态",
        "verb": "inspect",
        "intent": "service_recovery"
      }
    ],
    "special": "operations",
    "session": {
      "status": "未登录",
      "account": "demo.maintainer",
      "expires": "未建立",
      "verified": false
    },
    "sessionActions": [
      {
        "label": "进入运维",
        "verb": "activate",
        "intent": "operations_login",
        "confirm": true
      },
      {
        "label": "退出运维",
        "verb": "toggle",
        "intent": "operations_logout",
        "confirm": true
      }
    ],
    "guards": [
      "operations_session",
      "fresh_verification",
      "confirm_affected_scope"
    ],
    "recoverySteps": [
      "确认维护范围",
      "等待服务停止",
      "重新启动服务",
      "检查依赖",
      "确认恢复在线"
    ],
    "help": "服务操作需要运维身份验证。操作前请核对影响范围，提交后可查看恢复状态。"
  },
  {
    "id": "admin/diagnostics",
    "section": "admin",
    "title": "诊断与运行日志",
    "subtitle": "检查已登记目标的连通性，查询和打包运行日志。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "诊断项目"
      },
      {
        "key": "diagnosticType",
        "label": "类型"
      },
      {
        "key": "target",
        "label": "目标"
      },
      {
        "key": "status",
        "label": "状态"
      },
      {
        "key": "result",
        "label": "结果"
      },
      {
        "key": "lastRun",
        "label": "执行时间"
      }
    ],
    "rows": [
      {
        "id": "diagnostic-network-api",
        "domain": "platform",
        "name": "平台入口检查",
        "diagnosticType": "网络连接",
        "target": "api-01.example.test:443",
        "status": "已完成",
        "result": "连接成功 · 8 ms",
        "lastRun": "2026-10-10 06:10 UTC",
        "protocol": "TCP / TLS",
        "startTime": "2026-10-10 06:10",
        "endTime": "2026-10-10 06:11",
        "level": "全部级别",
        "addresses": "192.0.2.10",
        "detail": "DNS 解析与 TLS 握手完成，证书名称匹配。",
        "artifactReady": false,
        "authorizedTarget": "target-api",
        "checks": [
          "DNS 解析通过",
          "目标端口可达",
          "证书校验通过"
        ]
      },
      {
        "id": "diagnostic-network-db",
        "domain": "platform",
        "name": "数据库连接检查",
        "diagnosticType": "网络连接",
        "target": "db-01.example.test:5432",
        "status": "检查失败",
        "result": "目标端口未响应",
        "lastRun": "2026-10-10 05:40 UTC",
        "protocol": "TCP",
        "startTime": "2026-10-10 05:40",
        "endTime": "2026-10-10 05:41",
        "level": "全部级别",
        "addresses": "192.0.2.20",
        "detail": "上次检查未能建立连接。可重新检查并保留新结果。",
        "artifactReady": false,
        "authorizedTarget": "target-db",
        "checks": [
          "DNS 解析通过",
          "TCP 连接超时"
        ]
      },
      {
        "id": "diagnostic-log-api",
        "domain": "platform",
        "name": "API 运行日志",
        "diagnosticType": "平台日志",
        "target": "平台 API",
        "status": "已打包",
        "result": "128 条记录 · 42 KB",
        "lastRun": "2026-10-10 06:15 UTC",
        "protocol": "本地日志",
        "startTime": "2026-10-10 05:00",
        "endTime": "2026-10-10 06:00",
        "level": "WARN 与 ERROR",
        "detail": "时间窗口内的请求处理、连接异常与恢复记录。",
        "artifactReady": true,
        "artifactName": "api-diagnostics-20261010.txt",
        "redacted": true,
        "logLines": [
          "05:13:22 WARN 请求处理延迟升高，关联编号 req-demo-2001",
          "05:13:40 INFO 响应时延恢复，关联编号 req-demo-2001",
          "05:44:10 WARN 数据库连接等待，关联编号 req-demo-2002"
        ]
      },
      {
        "id": "diagnostic-log-worker",
        "domain": "platform",
        "name": "任务服务运行日志",
        "diagnosticType": "平台日志",
        "target": "任务服务",
        "status": "待打包",
        "result": "已选 1 小时时间窗口",
        "lastRun": "尚未执行",
        "protocol": "本地日志",
        "startTime": "2026-10-10 05:00",
        "endTime": "2026-10-10 06:00",
        "level": "全部级别",
        "detail": "任务领取、租约和结束确认日志。",
        "artifactReady": false,
        "artifactName": "worker-diagnostics-20261010.txt",
        "redacted": true,
        "logLines": [
          "05:16:01 INFO 任务领取成功，任务编号 task-demo-041",
          "05:16:24 INFO 任务结果已确认，任务编号 task-demo-041"
        ]
      }
    ],
    "fields": [
      {
        "key": "name",
        "label": "诊断名称",
        "type": "text",
        "required": true
      },
      {
        "key": "diagnosticType",
        "label": "诊断类型",
        "type": "select",
        "options": [
          "网络连接",
          "平台日志"
        ],
        "required": true
      },
      {
        "key": "target",
        "label": "目标",
        "type": "select",
        "options": [
          "api-01.example.test:443",
          "db-01.example.test:5432",
          "平台 API",
          "任务服务"
        ],
        "required": true
      },
      {
        "key": "startTime",
        "label": "开始时间",
        "type": "text"
      },
      {
        "key": "endTime",
        "label": "结束时间",
        "type": "text"
      },
      {
        "key": "level",
        "label": "日志级别",
        "type": "select",
        "options": [
          "全部级别",
          "WARN 与 ERROR",
          "仅 ERROR"
        ]
      }
    ],
    "primary": {
      "label": "新建诊断",
      "verb": "create"
    },
    "rowActions": [
      {
        "label": "执行诊断 / 打包",
        "verb": "test",
        "intent": "diagnostic_run"
      },
      {
        "label": "查看结果",
        "verb": "inspect",
        "intent": "diagnostic_result"
      },
      {
        "label": "下载诊断包",
        "verb": "download",
        "intent": "diagnostic_download",
        "requiresArtifact": true
      }
    ],
    "special": "diagnostics",
    "filterFields": [
      "diagnosticType",
      "target",
      "status",
      "level",
      "startTime",
      "endTime"
    ],
    "guards": [
      "registered_targets_only",
      "time_window_required_for_logs",
      "current_scope",
      "artifact_ready"
    ],
    "allowedTargets": [
      {
        "name": "api-01.example.test:443",
        "type": "网络连接",
        "domain": "platform"
      },
      {
        "name": "db-01.example.test:5432",
        "type": "网络连接",
        "domain": "platform"
      },
      {
        "name": "平台 API",
        "type": "平台日志",
        "domain": "platform"
      },
      {
        "name": "任务服务",
        "type": "平台日志",
        "domain": "platform"
      }
    ],
    "help": "只可检查已登记目标。日志下载遵循所选服务与时间范围，并隐藏认证信息。"
  },
  {
    "id": "admin/install",
    "section": "admin",
    "title": "安装与部署",
    "subtitle": "准备部署环境，检查依赖并跟踪安装步骤。",
    "scope": "platform",
    "kind": "table",
    "columns": [
      {
        "key": "name",
        "label": "部署名称"
      },
      {
        "key": "deploymentMode",
        "label": "部署方式"
      },
      {
        "key": "target",
        "label": "目标设备"
      },
      {
        "key": "version",
        "label": "版本"
      },
      {
        "key": "stage",
        "label": "当前步骤"
      },
      {
        "key": "status",
        "label": "状态"
      }
    ],
    "rows": [
      {
        "id": "installation-new",
        "domain": "platform",
        "name": "安全实验环境",
        "deploymentMode": "单节点部署",
        "target": "demo-appliance-02",
        "version": "1.4.2",
        "stage": "环境检查",
        "status": "待检查",
        "nodeCount": 1,
        "storageGB": 200,
        "os": "Linux 容器宿主机",
        "database": "PostgreSQL 17",
        "ports": "443 / 5432",
        "precheckPassed": false,
        "progress": 0,
        "rollbackAvailable": false,
        "impact": "新建隔离的测试环境，不影响现有平台。",
        "steps": [
          {
            "label": "确认设备",
            "status": "已完成"
          },
          {
            "label": "检查依赖",
            "status": "待执行"
          },
          {
            "label": "准备存储",
            "status": "待执行"
          },
          {
            "label": "安装服务",
            "status": "待执行"
          },
          {
            "label": "验证运行",
            "status": "待执行"
          }
        ]
      },
      {
        "id": "installation-distributed",
        "domain": "platform",
        "name": "安全运营环境",
        "deploymentMode": "独立 API 与任务节点",
        "target": "demo-appliance-01",
        "version": "1.4.2",
        "stage": "运行核对",
        "status": "已完成",
        "nodeCount": 2,
        "storageGB": 400,
        "os": "Linux 容器宿主机",
        "database": "PostgreSQL 17",
        "ports": "443 / 5432",
        "precheckPassed": true,
        "progress": 100,
        "rollbackAvailable": true,
        "impact": "已有平台。再次安装前需确认维护窗口与数据保留策略。",
        "steps": [
          {
            "label": "确认设备",
            "status": "已完成"
          },
          {
            "label": "检查依赖",
            "status": "已完成"
          },
          {
            "label": "准备存储",
            "status": "已完成"
          },
          {
            "label": "安装服务",
            "status": "已完成"
          },
          {
            "label": "验证运行",
            "status": "已完成"
          }
        ]
      },
      {
        "id": "installation-blocked",
        "domain": "platform",
        "name": "辅助分析环境",
        "deploymentMode": "单节点部署",
        "target": "demo-appliance-03",
        "version": "1.4.2",
        "stage": "存储检查",
        "status": "检查未通过",
        "nodeCount": 1,
        "storageGB": 40,
        "os": "Linux 容器宿主机",
        "database": "PostgreSQL 17",
        "ports": "443 / 5432",
        "precheckPassed": false,
        "progress": 20,
        "rollbackAvailable": false,
        "impact": "目标设备存储空间不足，需要调整容量后重新检查。",
        "steps": [
          {
            "label": "确认设备",
            "status": "已完成"
          },
          {
            "label": "检查依赖",
            "status": "已完成"
          },
          {
            "label": "准备存储",
            "status": "空间不足"
          },
          {
            "label": "安装服务",
            "status": "未开始"
          },
          {
            "label": "验证运行",
            "status": "未开始"
          }
        ]
      }
    ],
    "fields": [
      {
        "key": "name",
        "label": "部署名称",
        "type": "text",
        "required": true
      },
      {
        "key": "deploymentMode",
        "label": "部署方式",
        "type": "select",
        "options": [
          "单节点部署",
          "独立 API 与任务节点"
        ],
        "required": true
      },
      {
        "key": "target",
        "label": "目标设备",
        "type": "select",
        "options": [
          "demo-appliance-01",
          "demo-appliance-02",
          "demo-appliance-03"
        ],
        "required": true
      },
      {
        "key": "nodeCount",
        "label": "节点数量",
        "type": "number",
        "required": true,
        "min": 1,
        "max": 2
      },
      {
        "key": "storageGB",
        "label": "存储容量（GB）",
        "type": "number",
        "required": true,
        "min": 40,
        "max": 2000
      },
      {
        "key": "version",
        "label": "安装版本",
        "type": "select",
        "options": [
          "1.4.2"
        ],
        "required": true
      }
    ],
    "primary": {
      "label": "新建部署",
      "verb": "create"
    },
    "rowActions": [
      {
        "label": "检查环境",
        "verb": "test",
        "intent": "installation_precheck"
      },
      {
        "label": "开始安装",
        "verb": "activate",
        "intent": "installation_start",
        "confirm": true
      },
      {
        "label": "查看步骤",
        "verb": "inspect",
        "intent": "installation_steps"
      },
      {
        "label": "安装记录",
        "verb": "download",
        "intent": "installation_record"
      }
    ],
    "special": "install",
    "guards": [
      "precheck_required",
      "registered_targets_only",
      "confirm_affected_scope"
    ],
    "sections": [
      {
        "title": "安装前准备",
        "body": "确认设备、依赖和存储条件，再开始安装。已有部署需先核对维护范围。",
        "items": [
          {
            "label": "节点角色",
            "value": "API / 任务服务"
          },
          {
            "label": "运行依赖",
            "value": "PostgreSQL 17"
          },
          {
            "label": "设备选择",
            "value": "已登记设备"
          },
          {
            "label": "安装后核对",
            "value": "服务就绪、依赖可达、登录入口"
          }
        ]
      }
    ]
  }
];
