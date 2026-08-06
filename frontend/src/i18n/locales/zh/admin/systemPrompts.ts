export default {
  systemPrompts: {
    title: '业务系统提示词',
    description: '管理不可变提示词版本与全局运行策略。',
    runtime: {
      active: '已启用',
      disabled: '已关闭',
      degraded: '降级状态',
      bundleUnavailable: '技能包不可用',
      bundleDegraded: '技能包降级',
      enabled: '启用业务提示词',
      enabledHint: '仅作用于 OpenAI 及兼容适配器。',
      expose: '暴露服务端提示词',
      exposeHint: '允许客户端看到结构化响应 instructions。',
      compact: 'Compact 模式',
      compactHint: '独立控制本策略，不改变既有 Codex compact 兼容逻辑。'
    },
    bundle: {
      compositionMode: '组合模式',
      inline: '仅提示词正文',
      offline: '离线技能包',
      bundle: '技能包',
      select: '选择技能包',
      available: '可用',
      unavailable: '不可用',
      degraded: '降级',
      documents: '个文档',
      routes: '条路由'
    },
    templates: {
      title: '模板',
      count: '个模板',
      seed: '种子',
      empty: '暂无模板。',
      select: '选择模板查看版本。'
    },
    tabs: { editor: '编辑器', history: '版本历史', preview: '预览' },
    actions: {
      create: '创建模板',
      saveRuntime: '保存开关',
      saveMetadata: '保存元数据',
      saveDraft: '保存草稿',
      publish: '发布',
      rollback: '回滚',
      duplicate: '复制',
      delete: '删除',
      reload: '重新加载服务端状态',
      previewMerge: '预览合并',
      previewUpstream: '预览 upstream JSON'
    },
    editor: {
      name: '模板名称',
      description: '描述',
      descriptionPlaceholder: '可选的元数据描述',
      activeTemplate: '当前模板',
      updated: '更新于',
      unsaved: '未保存',
      raw: '原始文本',
      markdown: 'Markdown 预览',
      body: '提示词正文',
      note: '版本备注',
      notePlaceholder: '这次草稿改了什么？',
      bytes: 'UTF-8 字节数',
      versionCreated: '版本创建于'
    },
    history: {
      version: '版本',
      source: '组合来源',
      hash: 'SHA-256',
      size: '大小',
      note: '备注',
      created: '创建时间',
      actions: '操作',
      active: '生效中',
      empty: '暂无版本。'
    },
    preview: {
      mergeTitle: '指令合并',
      upstreamTitle: '最终 upstream JSON',
      client: '客户端指令',
      clientPlaceholder: '可选的客户端指令',
      protocol: '协议',
      compact: 'Compact',
      jsonBody: 'JSON 请求体',
      application: '应用元数据',
      effectiveHash: '有效提示词 SHA-256',
      routes: '命中路由',
      documents: '注入文档'
    },
    dialogs: {
      createTitle: '创建模板',
      duplicateTitle: '复制模板',
      slug: 'Slug',
      name: '名称',
      description: '描述',
      body: '初始提示词正文',
      note: '版本备注',
      copySuffix: '副本'
    },
    confirm: {
      publishTitle: '发布这个版本？',
      publishMessage: '发布会切换全局活动模板并递增运行时 revision。',
      rollbackTitle: '回滚到这个版本？',
      rollbackMessage: '回滚是显式且原子的，选中的不可变版本会成为活动版本。',
      deleteTitle: '删除这个模板？',
      deleteMessage: '模板会被软删除；种子模板和活动模板受保护。'
    },
    messages: {
      draftSaved: '草稿版本已保存。',
      metadataSaved: '模板元数据已保存。',
      runtimeSaved: '运行时开关已保存。',
      created: '模板已创建。',
      duplicated: '模板已复制。',
      published: '版本已发布。',
      rolledBack: '版本已回滚。',
      deleted: '模板已删除。'
    },
    errors: {
      load: '无法加载系统提示词运行状态。',
      loadDetail: '无法加载所选模板。',
      loadBundle: '无法加载离线技能包详情。',
      saveDraft: '无法保存草稿。',
      saveMetadata: '无法保存模板元数据。',
      saveRuntime: '无法保存运行时开关。',
      create: '无法创建模板。',
      duplicate: '无法复制模板。',
      publish: '无法发布版本。',
      rollback: '无法回滚版本。',
      delete: '无法删除模板。',
      preview: '无法生成预览。',
      invalidBody: '正文不能为空、不能包含 NUL 字节，且不得超过 64 KiB。',
      invalidJson: '请输入合法的 JSON 请求体。',
      conflict: '服务端 revision 已变化，请重新加载后再写入。',
      unsavedSelection: '切换选择前请先保存或放弃当前草稿。',
      saveBeforePublish: '请先保存草稿，再发布它。',
      bundleUnavailable: '所选离线技能包或固定 manifest 不可用。'
    }
  }
}
