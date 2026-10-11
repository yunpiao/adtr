export default function AssetDictionaryNavigation({
  version,
}: {
  version: 2 | 3;
}) {
  return (
    <nav className="asset-dictionary-navigation" aria-label="用户资产采集版本">
      <a
        href="#user-assets-v2"
        aria-current={version === 2 ? "page" : undefined}
      >
        字典 2 · 基础身份
      </a>
      <a
        href="#user-assets-v3"
        aria-current={version === 3 ? "page" : undefined}
      >
        字典 3 · 主组 RID / SPN
      </a>
    </nav>
  );
}
