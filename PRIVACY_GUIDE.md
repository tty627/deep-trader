# 隐私保护指南

## 已保护的敏感信息

以下文件已通过 `.gitignore` 排除，**不会**上传到 GitHub：

### 配置文件
- `config.local.json` - 包含你的 API 密钥
- `*.local.json` - 所有本地配置文件
- `.env` 和 `.env.local` - 环境变量文件
- `*.key`、`*.pem` - 密钥文件

### 运行时文件
- `trader.log` 和 `*.log` - 日志文件（可能包含敏感交易信息）
- `position_open_time.json` - 运行时数据
- `*.backup`、`*.bak` - 备份文件

### 编译产物
- `deep_trader.exe`、`simple_ai_trader` 等可执行文件
- 所有 `.exe` 文件

## 安全检查清单

在推送到 GitHub 之前，请确认：

1. ✅ `config.local.json` 已在 `.gitignore` 中
2. ✅ 从未提交过 `config.local.json` 到 Git 历史
3. ✅ 日志文件和运行时数据已排除
4. ✅ 代码中没有硬编码的 API 密钥

## 如何使用

### 首次设置
```bash
# 1. 复制示例配置文件
cp config.local.example.json config.local.json

# 2. 编辑 config.local.json 填入你的真实 API 密钥
# （此文件不会被 Git 跟踪）
```

### 推送到 GitHub
```bash
# 查看将要提交的文件
git status

# 添加文件（确保没有敏感文件）
git add .

# 提交
git commit -m "你的提交信息"

# 推送到 GitHub
git push origin master
```

### 验证隐私保护
```bash
# 检查某个文件是否被忽略
git check-ignore -v config.local.json

# 查看将要提交的文件列表
git ls-files

# 确保 config.local.json 不在列表中
```

## 重要提醒

⚠️ **永远不要**在代码中硬编码 API 密钥
⚠️ **永远不要**提交 `config.local.json` 文件
⚠️ 推送前**务必**运行 `git status` 检查

## 已采取的保护措施

1. ✅ 更新了 `.gitignore` 文件
2. ✅ 从 Git 跟踪中移除了 `trader.log` 和 `simple_ai_trader`
3. ✅ 验证了 `config.local.json` 从未被提交
4. ✅ 添加了完整的文件类型排除规则

## 如果不小心泄露了密钥

如果你已经将包含 API 密钥的文件推送到了 GitHub：

1. **立即更换** API 密钥（在币安和 DeepSeek 后台）
2. 使用 `git-filter-repo` 或 BFG Repo-Cleaner 从历史中移除敏感信息
3. 强制推送清理后的历史：`git push --force`

## 联系方式

如有疑问，请检查 GitHub 的安全文档：
https://docs.github.com/en/authentication/keeping-your-account-and-data-secure
