# 正式插件制品输入

正式发布工作流在 `current/` 生成签名 `.s2plugin` 包和 `lock.json`。镜像选择 `PLUGIN_BUNDLE_STAGE=plugin-bundle-release`，校验后原样复制这些制品；此目录不保存私钥。

`current/` 是构建输出，不提交源码。默认本地和候选镜像使用独立的开发签名阶段，正式 Codexrip 版本不能加载开发发布者签名的集合。
