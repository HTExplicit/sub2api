# Authorized Assessment and Tooling Module

## Enter When

- the user requests an authorized assessment workflow, scanner, analyzer, fuzzer, protocol client, detection utility, or reproducible security automation
- the task is broader than a single CTF technique but has explicit assets and success criteria

## Required Inputs

- scope and asset list
- authentication and test identities supplied for the assessment
- allowed state changes, rate/resource limits, and reset/cleanup method
- desired output schema and deployment/runtime constraints

## Do

1. Build a scope card and passive inventory before active checks.
2. Rank checks by evidence, likely impact, reproducibility, and cost.
3. For tools, define CLI/API inputs, structured outputs, dependencies, timeouts, concurrency, retries, and safe defaults before implementation.
4. Separate discovery from verification; a scanner match is not a confirmed finding.
5. Make target execution opt-in through explicit parameters. Do not enumerate unrelated local secrets or directories.
6. Add fixtures and deterministic tests for parsers, classifiers, and output schemas.

## Produce

- scoped test plan or complete runnable tool
- structured results with evidence and confidence
- reproducible finding verification
- cleanup/reset instructions
- tests and usage examples

## Verification

- run syntax/unit tests and representative fixtures
- test timeout, malformed input, empty input, network failure, and permission failure
- verify findings independently from the discovery signature
- ensure default execution does not modify targets or scan outside explicit scope

## Exit When

The assessment result or tool behaves correctly on positive, negative, and failure cases with explicit scope controls.

## Bundled Weaponry (pentest-tools 深度库)

进入实战渗透/漏洞挖掘阶段时，从并入的 `pentest-tools/` 深度库按需取用（每次只加载 1-2 个最相关文件，勿整库塞入上下文）：

- `pentest-tools/INSTRUCTIONS.md` — 库总入口：工具链（Nmap/Nuclei/SQLMap/FFUF/Hashcat/ZAP 等 20+ 工具的 MCP 工作流）
- `pentest-tools/src-hunter/INSTRUCTIONS.md` — 实战 SRC/众测 5 阶段方法论（intake→recon→enum→hunt→report）
- `pentest-tools/src-hunter/references/playbooks/` — 20 个攻击类 playbook：`rce.md`、`sqli.md`、`ssrf-cache-host.md`、`file-upload.md`、`oauth-saml-jwt.md`、`intranet-postexp.md`、`xss.md`、`graphql.md`、`http-smuggling.md`、`race-conditions.md`、`logic-flaws.md`、`api-rest.md`、`llm-prompt-injection.md` 等（先读 `00-index.md`）
- `pentest-tools/src-hunter/references/payloader/` — 分类 payload 库（`by-category/web/`、`by-category/intranet/`）、`waf-bypass.md`（WAF/EDR 绕过变体）、`tools/`、`raw/` 原始语料
- 挖洞结论进入验证/利用阶段时，转对应领域执行模块（Web→`../sec-web-api/`，Pwn→`../sec-pwn-native/`，链式→`../sec-attack-chain/`）

## Read

- `../../references/pentest.md`
- `../../references/scanner.md`
- `../../references/tools.md`
- `pentest-tools/INSTRUCTIONS.md` 与相关 playbook（进入实战渗透时）
- domain-specific module when a finding moves into exploitation or deep analysis
