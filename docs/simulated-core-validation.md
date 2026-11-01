# #251 首批模拟业务验证记录

基线：main 42f4db880c2c975f3c237e10b6af7462faa061bc。日期：2026-11-01。

## 已执行

- Go 1.27.1：`go test -race -count=1 ./...`、`go vet ./...` 通过。
- `python3 -m unittest discover -s scripts -p '*_contract.py'`：1202 项通过。
- `python3 scripts/verify_requirements.py`：209 项及来源/证据边界通过，未提高产品验收计数。
- `npm run typecheck --prefix web`、`npm run build --prefix web` 通过。
- `npm test --prefix web -- --run`：106 文件、4910 项通过。
- 前端使用已存在依赖副本，源/当前 package-lock SHA256 均为
  fb73eca2ec9a028891dd12cb85086d51663c25677651d007dfc798e751c60261。
  本轮未重新执行 npm ci 或依赖审计，不声称完整 make check 通过。
- CLI 七个命令跨模块测试通过；示例风险计算实际得到 score=30。
- 各包专门边界/权限/取消/短写测试及独立 race/vet 检查通过。

## 失败与修复

- 初次 CLI 风险测试使用 camelCase，但风险契约为 snake_case；严格解码拒绝。
  修正测试输入后通过，没有放宽解码。
- 独立复审发现 DNS Unicode Kelvin sign 在小写转换后混入 ASCII 域。
  改为先验证原始 ASCII，增加域/DNS/筛选输入回归，独立复现现已拒绝。
- CLI JSON 重复键原先可被后值覆盖；增加重复键和深度预算。
  复审又发现 long-s 与 encoding/json Unicode 折叠别名；入口进一步拒绝非 ASCII 键，
  独立复现现退出 2 且无结果，保留这两阶段发现。

## 独立代码复审

与实现者分离的复审检查了七包和 CLI，最终无待修具体问题；独立八目标 race/vet 通过。
最终复核文件 SHA256：
- cmd/adtr-simulate/main.go：6cdf16ac6f9916f1a3ce9864b6fd91f96e88de7fce64063433d992ced6d8e650
- internal/dnsipassets/records.go：281e023d86d25cdd95c1e4f361bbbdcab52e592dd212704dfc460115758507ae

## 尚未完成

本地 Docker 不可用，数据库/生命周期/浏览器集成未在本轮本地执行。
PR 精确提交 CI、正常合并及新 main 验证等待后续实际结果，不能继承历史 CI。
未接生产 HTTP/UI/数据库、真实身份或 AD。产品验收仍 0/209。
