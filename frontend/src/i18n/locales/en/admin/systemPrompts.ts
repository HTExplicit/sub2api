export default {
  systemPrompts: {
    title: 'Business System Prompts',
    description: 'Manage immutable prompt versions and the global runtime policy.',
    runtime: {
      active: 'Enabled',
      disabled: 'Disabled',
      degraded: 'Degraded',
      bundleUnavailable: 'Bundle unavailable',
      bundleDegraded: 'Bundle degraded',
      enabled: 'Enable business prompt',
      enabledHint: 'Applies only to OpenAI and compatible adapters.',
      expose: 'Expose server prompt',
      exposeHint: 'Keep structured response instructions visible to clients.',
      compact: 'Compact mode',
      compactHint: 'Controls this policy independently from legacy Codex compact handling.'
    },
    bundle: {
      compositionMode: 'Composition mode',
      inline: 'Prompt body only',
      offline: 'Offline skill bundle',
      bundle: 'Skill bundle',
      select: 'Select a skill bundle',
      available: 'Available',
      unavailable: 'Unavailable',
      degraded: 'Degraded',
      documents: 'documents',
      routes: 'routes'
    },
    templates: {
      title: 'Templates',
      count: 'templates',
      seed: 'Seed',
      empty: 'No templates available.',
      select: 'Select a template to inspect its versions.'
    },
    tabs: { editor: 'Editor', history: 'Version history', preview: 'Preview' },
    actions: {
      create: 'Create template',
      saveRuntime: 'Save switches',
      saveMetadata: 'Save metadata',
      saveDraft: 'Save draft',
      publish: 'Publish',
      rollback: 'Rollback',
      duplicate: 'Duplicate',
      delete: 'Delete',
      reload: 'Reload server state',
      previewMerge: 'Preview merge',
      previewUpstream: 'Preview upstream JSON'
    },
    editor: {
      name: 'Template name',
      description: 'Description',
      descriptionPlaceholder: 'Optional metadata description',
      activeTemplate: 'Active template',
      updated: 'Updated',
      unsaved: 'Unsaved',
      raw: 'Raw text',
      markdown: 'Markdown preview',
      body: 'Prompt body',
      note: 'Version note',
      notePlaceholder: 'What changed in this draft?',
      bytes: 'UTF-8 bytes',
      versionCreated: 'Version created'
    },
    history: {
      version: 'Version',
      source: 'Composition',
      hash: 'SHA-256',
      size: 'Size',
      note: 'Note',
      created: 'Created',
      actions: 'Actions',
      active: 'Active',
      empty: 'No versions available.'
    },
    preview: {
      mergeTitle: 'Instruction merge',
      upstreamTitle: 'Final upstream JSON',
      client: 'Client instructions',
      clientPlaceholder: 'Optional client instructions',
      protocol: 'Protocol',
      compact: 'Compact',
      jsonBody: 'JSON body',
      application: 'Application metadata',
      effectiveHash: 'Effective prompt SHA-256',
      routes: 'Matched routes',
      documents: 'Injected documents'
    },
    dialogs: {
      createTitle: 'Create template',
      duplicateTitle: 'Duplicate template',
      slug: 'Slug',
      name: 'Name',
      description: 'Description',
      body: 'Initial prompt body',
      note: 'Version note',
      copySuffix: 'copy'
    },
    confirm: {
      publishTitle: 'Publish this version?',
      publishMessage: 'Publishing changes the global active template and increments the runtime revision.',
      rollbackTitle: 'Rollback to this version?',
      rollbackMessage: 'Rollback is explicit and atomic. The selected immutable version will become active.',
      deleteTitle: 'Delete this template?',
      deleteMessage: 'The template will be soft-deleted. Seed and active templates are protected.'
    },
    messages: {
      draftSaved: 'Draft version saved.',
      metadataSaved: 'Template metadata saved.',
      runtimeSaved: 'Runtime switches saved.',
      created: 'Template created.',
      duplicated: 'Template duplicated.',
      published: 'Version published.',
      rolledBack: 'Version rolled back.',
      deleted: 'Template deleted.'
    },
    errors: {
      load: 'Unable to load system prompt runtime.',
      loadDetail: 'Unable to load the selected template.',
      loadBundle: 'Unable to load the offline skill bundle details.',
      saveDraft: 'Unable to save the draft.',
      saveMetadata: 'Unable to save template metadata.',
      saveRuntime: 'Unable to save runtime switches.',
      create: 'Unable to create the template.',
      duplicate: 'Unable to duplicate the template.',
      publish: 'Unable to publish the version.',
      rollback: 'Unable to roll back the version.',
      delete: 'Unable to delete the template.',
      preview: 'Unable to generate the preview.',
      invalidBody: 'Prompt body must be non-empty, contain no NUL bytes, and fit within 64 KiB.',
      invalidJson: 'Enter a valid JSON request body.',
      conflict: 'The server revision changed. Reload before making another write.',
      unsavedSelection: 'Save or discard the current draft before changing the selection.',
      saveBeforePublish: 'Save the draft before publishing it.',
      bundleUnavailable: 'The selected offline bundle or pinned manifest is unavailable.'
    }
  }
}
