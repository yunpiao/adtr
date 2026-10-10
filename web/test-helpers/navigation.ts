// Test-side expectations keep navigation migrations independent of production
// configuration: a missing route or group must fail, not silently disappear.
export const navigationGroups = [
  {
    id: "management",
    label: "平台管理",
    pages: ["访问管理", "资源与租户", "系统健康", "运行日志与诊断包"],
  },
  {
    id: "collection",
    label: "采集与接入",
    pages: [
      "域连接",
      "管理操作账户",
      "凭据授权清理",
      "目录资产",
      "目录读取授权",
      "补充目录资产",
      "补充目录凭据授权",
    ],
  },
  {
    id: "identity",
    label: "账户与身份",
    pages: ["账户概览", "个人资料", "修改密码", "多因素认证", "重置用户密码"],
  },
] as const;

export function navigationGroupFor(name: string) {
  return navigationGroups.find((group) =>
    (group.pages as readonly string[]).includes(name),
  );
}
