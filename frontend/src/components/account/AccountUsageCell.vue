<template>
  <div ref="rootRef" :data-usage-variant="variant">
    <template v-if="variant === 'compact'">
      <div v-if="compactLoading" class="flex h-5 items-center gap-1.5" data-test="usage-loading">
        <div class="h-3 w-12 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        <div class="h-3 w-16 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      </div>
      <div
        v-else-if="compactHasData"
        class="flex min-w-0 flex-wrap items-center gap-x-1 gap-y-0.5 text-[10px] leading-4"
        data-test="usage-compact-summary"
      >
        <span
          v-if="compactTextSummary"
          class="min-w-0 text-gray-600 dark:text-gray-300"
          :title="compactTextSummary"
        >
          {{ compactTextSummary }}
        </span>
        <template v-for="(segment, index) in compactUsageSegments" :key="segment.key">
          <span v-if="compactTextSummary || index > 0" class="text-gray-300 dark:text-gray-600">·</span>
          <UsageProgressBar
            :label="segment.label"
            :utilization="segment.utilization"
            :resets-at="segment.resetsAt"
            :remaining-capacity="segment.remainingCapacity"
            :color="segment.color"
            density="compact"
          />
        </template>
        <span
          v-if="compactPartialFailure"
          class="font-medium text-red-600 dark:text-red-400"
          data-test="usage-fetch-failed"
        >
          {{ t('admin.accounts.usageFetchFailed') }}
        </span>
        <span
          v-if="showStaleMarker"
          class="rounded bg-amber-100 px-1 py-0.5 font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
          data-test="usage-stale"
        >
          {{ t('admin.accounts.usageStale') }}
        </span>
      </div>
      <div
        v-else
        :class="[
          'text-xs',
          hasFetchFailureWithoutData ? 'text-red-500' : 'text-gray-400 dark:text-gray-500'
        ]"
        :data-test="hasFetchFailureWithoutData ? 'usage-fetch-failed' : 'usage-no-data'"
      >
        {{ emptyUsageText }}
      </div>
    </template>

    <template v-else-if="showUsageWindows">
    <!-- Anthropic OAuth and Setup Token accounts: fetch real usage data -->
    <template
      v-if="
        account.platform === 'anthropic' &&
        (account.type === 'oauth' || account.type === 'setup-token')
      "
    >
      <!-- Loading state -->
      <div v-if="loading && !usageInfo" class="space-y-1.5">
        <!-- OAuth: 3 rows, Setup Token: 1 row -->
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
        <template v-if="account.type === 'oauth'">
          <div class="flex items-center gap-1">
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          </div>
          <div class="flex items-center gap-1">
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          </div>
        </template>
      </div>

      <!-- Error state -->
      <div v-else-if="error && !usageInfo" class="text-xs text-red-500">
        {{ error }}
      </div>

      <!-- Usage data -->
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- API error (degraded response) -->
        <div v-if="usageInfo.error" class="text-xs text-amber-600 dark:text-amber-400 truncate max-w-[200px]" :title="usageInfo.error">
          {{ usageInfo.error }}
        </div>
        <!-- 5h Window -->
        <UsageProgressBar
          v-if="usageInfo.five_hour"
          :density="usageBarDensity"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          color="indigo"
        />

        <!-- 7d Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day"
          :density="usageBarDensity"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          color="emerald"
        />

        <!-- 7d Sonnet Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_sonnet"
          :density="usageBarDensity"
          label="7d S"
          :utilization="usageInfo.seven_day_sonnet.utilization"
          :resets-at="usageInfo.seven_day_sonnet.resets_at"
          color="purple"
        />

        <!-- 7d Fable Window (7d_oi) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_fable"
          :density="usageBarDensity"
          label="7d F"
          :utilization="usageInfo.seven_day_fable.utilization"
          :resets-at="usageInfo.seven_day_fable.resets_at"
          color="amber"
        />

        <!-- Passive sampling label + active query button -->
        <div class="flex items-center gap-1.5 mt-0.5">
          <span
            v-if="usageInfo.source === 'passive'"
            class="text-[9px] text-gray-400 dark:text-gray-500 italic"
          >
            {{ t('admin.accounts.usageWindow.passiveSampled') }}
          </span>
          <button
            v-if="canInteract"
            type="button"
            class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[9px] font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30 transition-colors"
            :disabled="activeQueryLoading"
            @click="loadActiveUsage"
          >
            <svg
              class="h-2.5 w-2.5"
              :class="{ 'animate-spin': activeQueryLoading }"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
              />
            </svg>
            {{ t('admin.accounts.usageWindow.activeQuery') }}
          </button>
        </div>
      </div>

      <!-- No data yet -->
      <div v-else class="space-y-1">
        <div class="text-xs text-gray-400">{{ emptyUsageText }}</div>
        <!-- Always allow on-demand upstream quota probe, even before passive headers exist. -->
        <GrokQuotaProbeCell v-if="canInteract" :account="account" />
      </div>
    </template>

    <!-- OpenAI OAuth accounts: single source from /usage API -->
    <template v-else-if="account.platform === 'openai' && account.type === 'oauth'">
      <div v-if="hasOpenAIUsageFallback" class="space-y-1">
        <UsageProgressBar
          v-if="usageInfo?.five_hour"
          :density="usageBarDensity"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          :show-now-when-idle="true"
          color="indigo"
        />
        <UsageProgressBar
          v-if="usageInfo?.seven_day"
          :density="usageBarDensity"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          :window-stats="usageInfo.seven_day.window_stats"
          :show-now-when-idle="true"
          color="emerald"
        />
        <!--
          Upstream codex /wham/usage quota query + reset. The local active-sampling
          refresh button is rendered via the pre-actions slot so the user sees a
          single row of related buttons instead of two stacked rows.
        -->
        <OpenAIQuotaResetCell
          v-if="canInteract"
          :account="account"
          @account-updated="handleQuotaResetAccountUpdated"
        >
          <template #pre-actions>
            <button
              type="button"
              class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="activeQueryLoading"
              @click="loadActiveUsage"
            >
              <svg
                class="h-2.5 w-2.5"
                :class="{ 'animate-spin': activeQueryLoading }"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
                />
              </svg>
              {{ t('admin.accounts.usageWindow.activeQuery') }}
            </button>
          </template>
        </OpenAIQuotaResetCell>
      </div>
      <div v-else-if="loading && !usageInfo" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
      </div>
      <div v-else>
        <div class="text-xs text-gray-400">{{ emptyUsageText }}</div>
        <!-- Always allow on-demand upstream quota query, even before local data exists. -->
        <OpenAIQuotaResetCell
          v-if="canInteract"
          :account="account"
          class="mt-1"
          @account-updated="handleQuotaResetAccountUpdated"
        />
      </div>
    </template>

    <!-- Antigravity OAuth accounts: fetch usage from API -->
    <template v-else-if="account.platform === 'antigravity' && account.type === 'oauth'">
      <!-- 账户类型徽章 -->
      <div v-if="antigravityTierLabel" class="mb-1 flex items-center gap-1">
        <span
          :class="[
            'inline-block rounded px-1.5 py-0.5 text-[10px] font-medium',
            antigravityTierClass
          ]"
        >
          {{ antigravityTierLabel }}
        </span>
        <!-- 不合格账户警告图标 -->
        <span
          v-if="hasIneligibleTiers"
          class="group relative cursor-help"
        >
          <svg
            class="h-3.5 w-3.5 text-red-500"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path
              fill-rule="evenodd"
              d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z"
              clip-rule="evenodd"
            />
          </svg>
          <span
            class="pointer-events-none absolute left-0 top-full z-50 mt-1 w-80 whitespace-normal break-words rounded bg-gray-900 px-3 py-2 text-xs leading-relaxed text-white opacity-0 shadow-lg transition-opacity group-hover:opacity-100 dark:bg-gray-700"
          >
            {{ t('admin.accounts.ineligibleWarning') }}
          </span>
        </span>
      </div>

      <!-- Forbidden state (403) -->
      <div v-if="isForbidden" class="space-y-1">
        <span
          :class="[
            'inline-block rounded px-1.5 py-0.5 text-[10px] font-medium',
            forbiddenBadgeClass
          ]"
        >
          {{ forbiddenLabel }}
        </span>
        <div v-if="canInteract && validationURL" class="flex items-center gap-1">
          <a
            :href="validationURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-[10px] text-blue-600 hover:text-blue-800 hover:underline dark:text-blue-400 dark:hover:text-blue-300"
            :title="t('admin.accounts.openVerification')"
          >
            {{ t('admin.accounts.openVerification') }}
          </a>
          <button
            type="button"
            class="text-[10px] text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
            :title="t('admin.accounts.copyLink')"
            @click="copyValidationURL"
          >
            {{ linkCopied ? t('admin.accounts.linkCopied') : t('admin.accounts.copyLink') }}
          </button>
        </div>
      </div>

      <!-- Needs reauth (401) -->
      <div v-else-if="needsReauth" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300">
          {{ t('admin.accounts.needsReauth') }}
        </span>
      </div>

      <!-- Degraded error (non-403, non-401) -->
      <div v-else-if="usageInfo?.error" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
          {{ usageErrorLabel }}
        </span>
      </div>

      <!-- Loading state -->
      <div v-else-if="loading && !usageInfo" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
      </div>

      <!-- Error state -->
      <div v-else-if="error && !usageInfo" class="text-xs text-red-500">
        {{ error }}
      </div>

      <!-- Usage data from API -->
      <div v-else-if="hasAntigravityQuotaFromAPI" class="space-y-1">
        <!-- Gemini 3 Pro -->
        <UsageProgressBar
          v-if="antigravity3ProUsageFromAPI !== null"
          :density="usageBarDensity"
          :label="t('admin.accounts.usageWindow.gemini3Pro')"
          :utilization="antigravity3ProUsageFromAPI.utilization"
          :resets-at="antigravity3ProUsageFromAPI.resetTime"
          color="indigo"
        />

        <!-- Gemini 3 Flash -->
        <UsageProgressBar
          v-if="antigravity3FlashUsageFromAPI !== null"
          :density="usageBarDensity"
          :label="t('admin.accounts.usageWindow.gemini3Flash')"
          :utilization="antigravity3FlashUsageFromAPI.utilization"
          :resets-at="antigravity3FlashUsageFromAPI.resetTime"
          color="emerald"
        />

        <!-- Gemini 3 Image -->
        <UsageProgressBar
          v-if="antigravity3ImageUsageFromAPI !== null"
          :density="usageBarDensity"
          :label="t('admin.accounts.usageWindow.gemini3Image')"
          :utilization="antigravity3ImageUsageFromAPI.utilization"
          :resets-at="antigravity3ImageUsageFromAPI.resetTime"
          color="purple"
        />

        <!-- Claude -->
        <UsageProgressBar
          v-if="antigravityClaudeUsageFromAPI !== null"
          :density="usageBarDensity"
          :label="t('admin.accounts.usageWindow.claude')"
          :utilization="antigravityClaudeUsageFromAPI.utilization"
          :resets-at="antigravityClaudeUsageFromAPI.resetTime"
          color="amber"
        />

        <div v-if="aiCreditsDisplay" class="mt-1 text-[10px] text-gray-500 dark:text-gray-400">
          💳 {{ t('admin.accounts.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
        </div>
      </div>
      <div v-else-if="aiCreditsDisplay" class="text-[10px] text-gray-500 dark:text-gray-400">
        💳 {{ t('admin.accounts.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
      </div>
      <div v-else class="text-xs text-gray-400">{{ emptyUsageText }}</div>
    </template>

    <!-- Grok OAuth accounts: passive xAI quota headers + local Sub2API usage -->
    <template v-else-if="account.platform === 'grok' && account.type === 'oauth'">
      <div v-if="loading && !usageInfo" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
      </div>
      <div v-else-if="error && !usageInfo" class="text-xs text-red-500">
        {{ error }}
      </div>
      <div v-else-if="needsReauth" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300">
          {{ t('admin.accounts.needsReauth') }}
        </span>
      </div>
      <div v-else-if="isForbidden" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300">
          {{ usageInfo?.grok_entitlement_status || t('admin.accounts.forbidden') }}
        </span>
      </div>
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- Free: only rolling 24h soft-gate bar. Paid: 7d + 30d + prepaid money. -->
        <template v-if="grokIsFree">
          <UsageProgressBar
            v-if="grokFreeTokenBar"
            :density="usageBarDensity"
            label="24h"
            :title="t('admin.accounts.usageWindow.grokFreeQuota24hHint', { limit: formatCompactNumber(grokFreeTokenBar.limit) })"
            :utilization="grokFreeTokenBar.utilization"
            :window-stats="grokFreeQuotaUsage"
            :show-now-when-idle="true"
            color="emerald"
          />
          <div v-else-if="grokQuotaUnknown" class="text-[10px] text-gray-500 dark:text-gray-400">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <template v-else>
          <UsageProgressBar
            v-if="grokWeeklyBillingBar"
            :density="usageBarDensity"
            label="7d"
            :utilization="grokWeeklyBillingBar.utilization"
            :resets-at="grokWeeklyBillingBar.resetsAt"
            :window-stats="grokWeeklyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <UsageProgressBar
            v-if="grokMonthlyBillingBar"
            :density="usageBarDensity"
            label="30d"
            :utilization="grokMonthlyBillingBar.utilization"
            :resets-at="grokMonthlyBillingBar.resetsAt"
            :window-stats="grokMonthlyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <div
            v-if="grokPrepaidMoneyLine"
            class="flex flex-wrap items-center gap-1 text-[10px] text-gray-500 dark:text-gray-400"
          >
            <span
              v-if="grokPrepaidMoneyLine.showPrepaid"
              class="rounded bg-emerald-50 px-1 py-0.5 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
              :title="t('admin.accounts.usageWindow.grokPrepaid')"
            >
              {{ t('admin.accounts.usageWindow.grokPrepaid') }} ${{ grokPrepaidMoneyLine.prepaid }}
            </span>
            <span
              v-if="grokPrepaidMoneyLine.showUsedLimit"
              :title="t('admin.accounts.usageWindow.grokMonthlyLimit')"
            >
              {{ t('admin.accounts.usageWindow.grokUsed') }}
              {{ grokPrepaidMoneyLine.used }}/{{ grokPrepaidMoneyLine.limit }}
            </span>
          </div>
          <div v-if="grokQuotaUnknown" class="text-[10px] text-gray-500 dark:text-gray-400">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <div v-if="usageInfo.error" class="truncate text-xs text-amber-600 dark:text-amber-400 max-w-[200px]" :title="usageInfo.error">
          {{ usageErrorLabel }}
        </div>
        <div v-if="grokRetryAfterLabel" class="text-[10px] text-amber-600 dark:text-amber-400">
          {{ t('admin.accounts.usageWindow.grokRetryAfter', { time: grokRetryAfterLabel }) }}
        </div>
        <div v-if="grokQuotaStatusLine" class="text-[10px] text-gray-500 dark:text-gray-400">
          {{ grokQuotaStatusLine }}
        </div>
        <GrokQuotaProbeCell v-if="canInteract" :account="account" @probed="handleGrokProbed" />
      </div>
      <div v-else class="space-y-1">
        <div class="text-xs text-gray-400">{{ emptyUsageText }}</div>
        <GrokQuotaProbeCell v-if="canInteract" :account="account" compact @probed="handleGrokProbed" />
      </div>
    </template>

    <!-- CN providers (Kimi / Zhipu / DeepSeek): coding-plan quota or payg balance -->
    <template v-else-if="account.platform === 'kimi' || account.platform === 'zhipu' || account.platform === 'deepseek'">
      <div class="space-y-1">
        <!-- 子单元格各自按 模式×平台 判定可见；两者都不可见时（智谱 payg 无公开
             余额端点、coding 探测也不适用）才回落到占位符。 -->
        <div
          v-if="!cnQuotaCellVisible && !cnBalanceCellVisible"
          class="text-xs text-gray-400"
          :title="t('admin.accounts.cnProviders.noBalanceEndpoint')"
        >-</div>
        <CNProviderQuotaCell :account="account" />
        <CNProviderBalanceCell :account="account" />
      </div>
    </template>

    <!-- Gemini platform: show quota + local usage window -->
    <template v-else-if="account.platform === 'gemini'">
      <!-- Auth Type + Tier Badge (first line) -->
      <div v-if="geminiAuthTypeLabel" class="mb-1 flex items-center gap-1">
        <span
          :class="[
            'inline-block rounded px-1.5 py-0.5 text-[10px] font-medium',
            geminiTierClass
          ]"
        >
          {{ geminiAuthTypeLabel }}
        </span>
        <!-- Help icon -->
        <span
          class="group relative cursor-help"
        >
          <svg
            class="h-3.5 w-3.5 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path
              fill-rule="evenodd"
              d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-3a1 1 0 00-.867.5 1 1 0 11-1.731-1A3 3 0 0113 8a3.001 3.001 0 01-2 2.83V11a1 1 0 11-2 0v-1a1 1 0 011-1 1 1 0 100-2zm0 8a1 1 0 100-2 1 1 0 000 2z"
              clip-rule="evenodd"
            />
          </svg>
          <span
            class="pointer-events-none absolute left-0 top-full z-50 mt-1 w-80 whitespace-normal break-words rounded bg-gray-900 px-3 py-2 text-xs leading-relaxed text-white opacity-0 shadow-lg transition-opacity group-hover:opacity-100 dark:bg-gray-700"
          >
            <div class="font-semibold mb-1">{{ t('admin.accounts.gemini.quotaPolicy.title') }}</div>
            <div class="mb-2 text-gray-300">{{ t('admin.accounts.gemini.quotaPolicy.note') }}</div>
            <div class="space-y-1">
              <div><strong>{{ geminiQuotaPolicyChannel }}:</strong></div>
              <div class="pl-2">• {{ geminiQuotaPolicyLimits }}</div>
              <div class="mt-2">
                <a :href="geminiQuotaPolicyDocsUrl" target="_blank" rel="noopener noreferrer" class="text-blue-400 hover:text-blue-300 underline">
                  {{ t('admin.accounts.gemini.quotaPolicy.columns.docs') }} →
                </a>
              </div>
            </div>
          </span>
        </span>
      </div>

      <!-- Usage data or unlimited flow -->
      <div class="space-y-1">
        <div
          v-if="showGeminiTodayStats && todayStats"
          class="mb-0.5 flex items-center"
        >
          <div class="flex flex-wrap items-center gap-1.5 text-[9px] text-gray-500 dark:text-gray-400">
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
              {{ formatKeyRequests }} req
            </span>
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
              {{ formatKeyTokens }}
            </span>
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800" :title="t('usage.accountBilled')">
              A ${{ formatKeyCost }}
            </span>
            <span
              v-if="todayStats.user_cost != null"
              class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
              :title="t('usage.userBilled')"
            >
              U ${{ formatKeyUserCost }}
            </span>
          </div>
        </div>
        <div
          v-else-if="showGeminiTodayStats && todayStatsLoading"
          class="mb-0.5 flex items-center gap-1"
        >
          <div class="h-3 w-10 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-8 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-12 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
        <div v-if="loading && !usageInfo" class="space-y-1">
          <div class="flex items-center gap-1">
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          </div>
        </div>
        <div v-else-if="error && !usageInfo" class="text-xs text-red-500">
          {{ error }}
        </div>
        <!-- Gemini: show daily usage bars when available -->
        <div v-else-if="geminiUsageAvailable" class="space-y-1">
          <UsageProgressBar
            v-for="bar in geminiUsageBars"
            :key="bar.key"
            :density="usageBarDensity"
            :label="bar.label"
            :utilization="bar.utilization"
            :resets-at="bar.resetsAt"
            :window-stats="bar.windowStats"
            :color="bar.color"
          />
          <p class="mt-1 text-[9px] leading-tight text-gray-400 dark:text-gray-500 italic">
            * {{ t('admin.accounts.gemini.quotaPolicy.simulatedNote') || 'Simulated quota' }}
          </p>
        </div>
        <!-- AI Studio Client OAuth: show unlimited flow (no usage tracking) -->
        <div v-else-if="account.type !== 'apikey' || !hasApiKeyQuota" class="text-xs text-gray-400">
          {{ t('admin.accounts.gemini.rateLimit.unlimited') }}
        </div>
        <UsageProgressBar
          v-if="quotaDailyBar"
          :density="usageBarDensity"
          label="1d"
          :utilization="quotaDailyBar.utilization"
          :resets-at="quotaDailyBar.resetsAt"
          color="indigo"
        />
        <UsageProgressBar
          v-if="quotaWeeklyBar"
          :density="usageBarDensity"
          label="7d"
          :utilization="quotaWeeklyBar.utilization"
          :resets-at="quotaWeeklyBar.resetsAt"
          color="emerald"
        />
        <UsageProgressBar
          v-if="quotaTotalBar"
          :density="usageBarDensity"
          label="total"
          :utilization="quotaTotalBar.utilization"
          color="purple"
        />
      </div>
    </template>

    <!-- Other accounts: no usage window -->
    <template v-else>
      <div class="text-xs text-gray-400">{{ emptyUsageText }}</div>
    </template>
    </template>

    <!-- Non-OAuth/Setup-Token accounts -->
    <template v-else>
    <!-- Gemini API Key accounts: show quota info -->
    <AccountQuotaInfo v-if="account.platform === 'gemini'" :account="account" />
    <!-- Key/Bedrock accounts: show today stats + optional quota bars -->
    <div v-else class="space-y-1">
      <OllamaCloudUsageCell
        v-if="account.ollama_cloud_usage?.eligible"
        :account="account"
        :density="usageBarDensity"
        @updated="handleOllamaCloudUsageUpdated"
      />
      <!-- Today stats row (requests, tokens, cost, user_cost) -->
      <div
        v-if="todayStats"
        class="mb-0.5 flex items-center"
      >
        <div class="flex flex-wrap items-center gap-1.5 text-[9px] text-gray-500 dark:text-gray-400">
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
            {{ formatKeyRequests }} req
          </span>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
            {{ formatKeyTokens }}
          </span>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800" :title="t('usage.accountBilled')">
            A ${{ formatKeyCost }}
          </span>
          <span
            v-if="todayStats.user_cost != null"
            class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
            :title="t('usage.userBilled')"
          >
            U ${{ formatKeyUserCost }}
          </span>
        </div>
      </div>
      <!-- Loading skeleton for today stats -->
      <div
        v-else-if="todayStatsLoading"
        class="mb-0.5 flex items-center gap-1"
      >
        <div class="h-3 w-10 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        <div class="h-3 w-8 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        <div class="h-3 w-12 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      </div>

      <!-- API Key accounts with quota limits: show progress bars -->
      <UsageProgressBar
        v-if="quotaDailyBar"
        :density="usageBarDensity"
        label="1d"
        :utilization="quotaDailyBar.utilization"
        :resets-at="quotaDailyBar.resetsAt"
        color="indigo"
      />
      <UsageProgressBar
        v-if="quotaWeeklyBar"
        :density="usageBarDensity"
        label="7d"
        :utilization="quotaWeeklyBar.utilization"
        :resets-at="quotaWeeklyBar.resetsAt"
        color="emerald"
      />
      <UsageProgressBar
        v-if="quotaTotalBar"
        :density="usageBarDensity"
        label="total"
        :utilization="quotaTotalBar.utilization"
        color="purple"
      />

      <!-- No data at all -->
      <div
        v-if="!todayStats && !todayStatsLoading && !hasApiKeyQuota && !account.ollama_cloud_usage?.eligible"
        class="text-xs text-gray-400"
      >{{ emptyUsageText }}</div>
    </div>
    </template>

    <div
      v-if="variant !== 'compact' && showStaleMarker"
      class="mt-1 text-[10px] font-medium text-amber-600 dark:text-amber-400"
      data-test="usage-stale"
    >
      {{ t('admin.accounts.usageStale') }}
    </div>
    <div
      v-if="variant !== 'compact' && compactPartialFailure"
      class="mt-1 text-[10px] font-medium text-red-600 dark:text-red-400"
      data-test="usage-fetch-failed"
    >
      {{ t('admin.accounts.usageFetchFailed') }}
    </div>
  </div>
</template>

<script lang="ts">
// These caches must live at module scope so table/card/drawer instances share them.
const _usageCache = new Map<number, {
  data: import('@/types').AccountUsageInfo
  ts: number
  failedAt?: number
}>()
const _usageRequests = new Map<number, {
  force: boolean
  promise: Promise<import('@/types').AccountUsageInfo>
}>()
</script>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { GrokQuotaProbeResult } from '@/api/admin/grok'
import type { Account, AccountUsageInfo, GeminiCredentials, WindowStats } from '@/types'
import { buildOpenAIUsageRefreshKey } from '@/utils/accountUsageRefresh'
import { enqueueUsageRequest } from '@/utils/usageLoadQueue'
import { formatCompactNumber, formatRelativeTime } from '@/utils/format'
import UsageProgressBar from './UsageProgressBar.vue'
import AccountQuotaInfo from './AccountQuotaInfo.vue'
import OpenAIQuotaResetCell from './OpenAIQuotaResetCell.vue'
import GrokQuotaProbeCell from './GrokQuotaProbeCell.vue'
import CNProviderQuotaCell from './CNProviderQuotaCell.vue'
import CNProviderBalanceCell from './CNProviderBalanceCell.vue'
import OllamaCloudUsageCell from './OllamaCloudUsageCell.vue'
import { cnQuotaCellVisible as cnQuotaCellVisibleFn, cnBalanceCellVisible as cnBalanceCellVisibleFn } from './credentialsBuilder'

const USAGE_CACHE_TTL = 5 * 60 * 1000 // 5 minutes
const USAGE_STALE_AFTER = 15 * 60 * 1000

type UsageVariant = 'detail' | 'list' | 'compact'

const requestUsage = async (
  account: Account,
  source?: 'passive' | 'active',
  force = false
): Promise<AccountUsageInfo> => {
  const existing = _usageRequests.get(account.id)
  if (existing) {
    if (!force || existing.force) return existing.promise
    // A manual force refresh must not be swallowed by an older passive request.
    try {
      await existing.promise
    } catch {
      // The explicit refresh below is still allowed to recover.
    }
  }

  const promise = enqueueUsageRequest(account, () => {
    if (force) return adminAPI.accounts.getUsage(account.id, source, true)
    if (source) return adminAPI.accounts.getUsage(account.id, source)
    return adminAPI.accounts.getUsage(account.id)
  })
  _usageRequests.set(account.id, { force, promise })
  try {
    return await promise
  } finally {
    if (_usageRequests.get(account.id)?.promise === promise) {
      _usageRequests.delete(account.id)
    }
  }
}
// How long a quota-reset response may suppress the row-patch usage refetch.
const SUPPRESS_USAGE_REFRESH_WINDOW_MS = 5 * 1000

const props = withDefaults(
  defineProps<{
    account: Account
    todayStats?: WindowStats | null
    todayStatsLoading?: boolean
    todayStatsError?: boolean
    todayStatsUpdatedAt?: number | null
    manualRefreshToken?: number
    statusNow?: number
    variant?: UsageVariant
    readOnly?: boolean
    batchedUsage?: AccountUsageInfo | null
    batchedUsageError?: string | null
    batchedUsageLoading?: boolean
    requestBatchedUsage?: ((account: Account, options?: { force?: boolean }) => void) | null
  }>(),
  {
    todayStats: null,
    todayStatsLoading: false,
    todayStatsError: false,
    todayStatsUpdatedAt: null,
    manualRefreshToken: 0,
    statusNow: Date.now(),
    variant: 'detail',
    readOnly: false,
    batchedUsage: null,
    batchedUsageError: null,
    batchedUsageLoading: false,
    requestBatchedUsage: null
  }
)

const emit = defineEmits<{
  'account-updated': [account: Account]
  'usage-loaded': [usage: AccountUsageInfo]
}>()

const { t } = useI18n()
const desktopViewportQuery = '(min-width: 768px)'

const unmounted = ref(false)
onBeforeUnmount(() => { unmounted.value = true })

const loading = ref(false)
const activeQueryLoading = ref(false)
const error = ref<string | null>(null)
const usageInfo = ref<AccountUsageInfo | null>(null)
const usageLastSuccessAt = ref<number | null>(null)
watch(usageInfo, (usage) => {
  if (usage) emit('usage-loaded', usage)
})
const suppressOpenAIUsageRefreshUntil = ref(0)
const rootRef = ref<HTMLElement | null>(null)
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(desktopViewportQuery).matches
)
const hasEnteredViewport = ref(false)
const pendingAutoLoad = ref(false)
const pendingAutoLoadSource = ref<'passive' | 'active' | undefined>(undefined)

let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null
let visibilityObserver: IntersectionObserver | null = null

const canInteract = computed(() => props.variant === 'detail' && !props.readOnly)
const usageBarDensity = computed<'detail' | 'list'>(() => (
  props.variant === 'list' ? 'list' : 'detail'
))

// Show usage windows for OAuth and Setup Token accounts
const showUsageWindows = computed(() => {
  // Gemini: we can always compute local usage windows from DB logs (simulated quotas).
  if (props.account.platform === 'gemini') return true
  // CN providers: apikey 账号也有滚动用量窗口（coding plan）或余额（payg），
  // 由 CNProviderQuotaCell / CNProviderBalanceCell 自行探测与展示。
  if (
    props.account.platform === 'kimi' ||
    props.account.platform === 'zhipu' ||
    props.account.platform === 'deepseek'
  ) {
    return true
  }
  return props.account.type === 'oauth' || props.account.type === 'setup-token'
})

const shouldFetchUsage = computed(() => {
  if (props.account.platform === 'anthropic') {
    return props.account.type === 'oauth' || props.account.type === 'setup-token'
  }
  if (props.account.platform === 'gemini') {
    return true
  }
  if (props.account.platform === 'antigravity') {
    return props.account.type === 'oauth'
  }
  if (props.account.platform === 'grok') {
    return props.account.type === 'oauth'
  }
  if (props.account.platform === 'openai') {
    return props.account.type === 'oauth'
  }
  return false
})

// CN 供应商子单元格可见性（与 CNProviderQuotaCell / CNProviderBalanceCell 共用
// credentialsBuilder 的单一实现）：都不可见时显示 `-` 占位符。
const cnAccountMode = computed(() => {
  const mode = props.account.credentials?.account_mode
  return typeof mode === 'string' ? mode : ''
})
const cnQuotaCellVisible = computed(() => cnQuotaCellVisibleFn(props.account.platform, cnAccountMode.value))
const cnBalanceCellVisible = computed(() => cnBalanceCellVisibleFn(props.account.platform, cnAccountMode.value))

const isBatchManaged = computed(() => typeof props.requestBatchedUsage === 'function')

const showGeminiTodayStats = computed(() => {
  return props.account.platform === 'gemini' &&
    (props.account.type === 'service_account' || props.account.type === 'apikey')
})

const geminiUsageAvailable = computed(() => {
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_pro_daily ||
    !!usageInfo.value?.gemini_flash_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    !!usageInfo.value?.gemini_pro_minute ||
    !!usageInfo.value?.gemini_flash_minute
  )
})

const hasOpenAIUsageFallback = computed(() => {
  if (props.account.platform !== 'openai' || props.account.type !== 'oauth') return false
  return !!usageInfo.value?.five_hour || !!usageInfo.value?.seven_day
})

const openAIUsageRefreshKey = computed(() => buildOpenAIUsageRefreshKey(props.account))

const shouldAutoLoadUsageOnMount = computed(() => {
  return shouldFetchUsage.value
})

const shouldLazyLoadOnMobile = computed(() => {
  return shouldFetchUsage.value && !isDesktopViewport.value
})

// Antigravity quota types (用于 API 返回的数据)
interface AntigravityUsageResult {
  utilization: number
  resetTime: string | null
}

// ===== Antigravity quota from API (usageInfo.antigravity_quota) =====

// 检查是否有从 API 获取的配额数据
const hasAntigravityQuotaFromAPI = computed(() => {
  return usageInfo.value?.antigravity_quota && Object.keys(usageInfo.value.antigravity_quota).length > 0
})

// 从 API 配额数据中获取使用率（多模型取最高使用率）
const getAntigravityUsageFromAPI = (
  modelNames: string[]
): AntigravityUsageResult | null => {
  const quota = usageInfo.value?.antigravity_quota
  if (!quota) return null

  let maxUtilization = 0
  let earliestReset: string | null = null

  for (const model of modelNames) {
    const modelQuota = quota[model]
    if (!modelQuota) continue

    if (modelQuota.utilization > maxUtilization) {
      maxUtilization = modelQuota.utilization
    }
    if (modelQuota.reset_time) {
      if (!earliestReset || modelQuota.reset_time < earliestReset) {
        earliestReset = modelQuota.reset_time
      }
    }
  }

  // 如果没有找到任何匹配的模型
  if (maxUtilization === 0 && earliestReset === null) {
    const hasAnyData = modelNames.some((m) => quota[m])
    if (!hasAnyData) return null
  }

  return {
    utilization: maxUtilization,
    resetTime: earliestReset
  }
}

// Gemini 3 Pro from API
const antigravity3ProUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-3-pro-low', 'gemini-3-pro-high', 'gemini-3-pro-preview'])
)

// Gemini 3 Flash from API
const antigravity3FlashUsageFromAPI = computed(() => getAntigravityUsageFromAPI(['gemini-3-flash']))

// Gemini Image from API
const antigravity3ImageUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-2.5-flash-image', 'gemini-3.1-flash-image', 'gemini-3-pro-image'])
)

// Claude from API (all Claude model variants)
const antigravityClaudeUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI([
    'claude-fable-5',
    'claude-sonnet-4-5', 'claude-opus-4-5-thinking',
    'claude-sonnet-4-6', 'claude-opus-4-6', 'claude-opus-4-6-thinking',
    'claude-opus-4-7', 'claude-opus-4-8',
  ])
)

const aiCreditsDisplay = computed(() => {
  const credits = usageInfo.value?.ai_credits
  if (!credits || credits.length === 0) return null
  const total = credits.reduce((sum, credit) => sum + (credit.amount ?? 0), 0)
  if (total <= 0) return null
  return total.toFixed(0)
})

// Antigravity 账户类型（从 load_code_assist 响应中提取）
const antigravityTier = computed(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  if (!extra) return null

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return null

  // 优先取 paidTier，否则取 currentTier
  const paidTier = loadCodeAssist.paidTier as Record<string, unknown> | undefined
  if (paidTier && typeof paidTier.id === 'string') {
    return paidTier.id
  }

  const currentTier = loadCodeAssist.currentTier as Record<string, unknown> | undefined
  if (currentTier && typeof currentTier.id === 'string') {
    return currentTier.id
  }

  return null
})

// Gemini 账户类型（从 credentials 中提取）
const geminiTier = computed(() => {
  if (props.account.platform !== 'gemini') return null
  const creds = props.account.credentials as GeminiCredentials | undefined
  return creds?.tier_id || null
})

const geminiOAuthType = computed(() => {
  if (props.account.platform !== 'gemini') return null
  const creds = props.account.credentials as GeminiCredentials | undefined
  return (creds?.oauth_type || '').trim() || null
})

// Gemini 是否为 Code Assist OAuth
const isGeminiCodeAssist = computed(() => {
  if (props.account.platform !== 'gemini') return false
  const creds = props.account.credentials as GeminiCredentials | undefined
  return creds?.oauth_type === 'code_assist' || (!creds?.oauth_type && !!creds?.project_id)
})

const geminiChannelShort = computed((): 'ai studio' | 'gcp' | 'google one' | 'client' | null => {
  if (props.account.platform !== 'gemini') return null

  // API Key accounts are AI Studio.
  if (props.account.type === 'apikey') return 'ai studio'

  if (geminiOAuthType.value === 'google_one') return 'google one'
  if (isGeminiCodeAssist.value) return 'gcp'
  if (geminiOAuthType.value === 'ai_studio') return 'client'

  // Fallback (unknown legacy data): treat as AI Studio.
  return 'ai studio'
})

const geminiUserLevel = computed((): string | null => {
  if (props.account.platform !== 'gemini') return null

  const tier = (geminiTier.value || '').toString().trim()
  const tierLower = tier.toLowerCase()
  const tierUpper = tier.toUpperCase()

  // Google One: free / pro / ultra
  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_one_free') return 'free'
    if (tierLower === 'google_ai_pro') return 'pro'
    if (tierLower === 'google_ai_ultra') return 'ultra'

    // Backward compatibility (legacy tier markers)
    if (tierUpper === 'AI_PREMIUM' || tierUpper === 'GOOGLE_ONE_STANDARD') return 'pro'
    if (tierUpper === 'GOOGLE_ONE_UNLIMITED') return 'ultra'
    if (tierUpper === 'FREE' || tierUpper === 'GOOGLE_ONE_BASIC' || tierUpper === 'GOOGLE_ONE_UNKNOWN' || tierUpper === '') return 'free'

    return null
  }

  // GCP Code Assist: standard / enterprise
  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise') return 'enterprise'
    if (tierLower === 'gcp_standard') return 'standard'

    // Backward compatibility
    if (tierUpper.includes('ULTRA') || tierUpper.includes('ENTERPRISE')) return 'enterprise'
    return 'standard'
  }

  // AI Studio (API Key) and Client OAuth: free / paid
  if (props.account.type === 'apikey' || geminiOAuthType.value === 'ai_studio') {
    if (tierLower === 'aistudio_paid') return 'paid'
    if (tierLower === 'aistudio_free') return 'free'

    // Backward compatibility
    if (tierUpper.includes('PAID') || tierUpper.includes('PAYG') || tierUpper.includes('PAY')) return 'paid'
    if (tierUpper.includes('FREE')) return 'free'
    if (props.account.type === 'apikey') return 'free'
    return null
  }

  return null
})

// Gemini 认证类型（按要求：授权方式简称 + 用户等级）
const geminiAuthTypeLabel = computed(() => {
  if (props.account.platform !== 'gemini') return null
  if (!geminiChannelShort.value) return null
  return geminiUserLevel.value ? `${geminiChannelShort.value} ${geminiUserLevel.value}` : geminiChannelShort.value
})

// Gemini 账户类型徽章样式（统一样式）
const geminiTierClass = computed(() => {
  // Use channel+level to choose a stable color without depending on raw tier_id variants.
  const channel = geminiChannelShort.value
  const level = geminiUserLevel.value

  if (channel === 'client' || channel === 'ai studio') {
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  if (channel === 'google one') {
    if (level === 'ultra') return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    if (level === 'pro') return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    return 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
  }

  if (channel === 'gcp') {
    if (level === 'enterprise') return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  return ''
})

// Gemini 配额政策信息
const geminiQuotaPolicyChannel = computed(() => {
  if (geminiOAuthType.value === 'google_one') {
    return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.channel')
  }
  if (isGeminiCodeAssist.value) {
    return t('admin.accounts.gemini.quotaPolicy.rows.gcp.channel')
  }
  return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.channel')
})

const geminiQuotaPolicyLimits = computed(() => {
  const tierLower = (geminiTier.value || '').toString().trim().toLowerCase()

  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_ai_ultra' || geminiUserLevel.value === 'ultra') {
      return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsUltra')
    }
    if (tierLower === 'google_ai_pro' || geminiUserLevel.value === 'pro') {
      return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsPro')
    }
    return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsFree')
  }

  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise' || geminiUserLevel.value === 'enterprise') {
      return t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsEnterprise')
    }
    return t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsStandard')
  }

  // AI Studio (API Key / custom OAuth)
  if (tierLower === 'aistudio_paid' || geminiUserLevel.value === 'paid') {
    return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsPaid')
  }
  return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsFree')
})

const geminiQuotaPolicyDocsUrl = computed(() => {
  if (geminiOAuthType.value === 'google_one' || isGeminiCodeAssist.value) {
    return 'https://developers.google.com/gemini-code-assist/resources/quotas'
  }
  return 'https://ai.google.dev/pricing'
})

const geminiUsesSharedDaily = computed(() => {
  if (props.account.platform !== 'gemini') return false
  // Per requirement: Google One & GCP are shared RPD pools (no per-model breakdown).
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    geminiOAuthType.value === 'google_one' ||
    isGeminiCodeAssist.value
  )
})

const geminiUsageBars = computed(() => {
  if (props.account.platform !== 'gemini') return []
  if (!usageInfo.value) return []

  const bars: Array<{
    key: string
    label: string
    utilization: number
    resetsAt: string | null
    windowStats?: WindowStats | null
    color: 'indigo' | 'emerald'
  }> = []

  if (geminiUsesSharedDaily.value) {
    const sharedDaily = usageInfo.value.gemini_shared_daily
    if (sharedDaily) {
      bars.push({
        key: 'shared_daily',
        label: '1d',
        utilization: sharedDaily.utilization,
        resetsAt: sharedDaily.resets_at,
        windowStats: sharedDaily.window_stats,
        color: 'indigo'
      })
    }
    return bars
  }

  const pro = usageInfo.value.gemini_pro_daily
  if (pro) {
    bars.push({
      key: 'pro_daily',
      label: 'pro',
      utilization: pro.utilization,
      resetsAt: pro.resets_at,
      windowStats: pro.window_stats,
      color: 'indigo'
      })
  }

  const flash = usageInfo.value.gemini_flash_daily
  if (flash) {
    bars.push({
      key: 'flash_daily',
      label: 'flash',
      utilization: flash.utilization,
      resetsAt: flash.resets_at,
      windowStats: flash.window_stats,
      color: 'emerald'
    })
  }

  return bars
})

interface GrokQuotaBarInfo {
  utilization: number
  resetsAt: string | null
  windowStats?: WindowStats | null
}

const makeGrokQuotaBar = (quota?: { limit?: number | null; remaining?: number | null; reset_at?: string | null } | null): GrokQuotaBarInfo | null => {
  if (!quota || quota.limit == null || quota.remaining == null || quota.limit <= 0) return null
  const remaining = Math.min(quota.limit, Math.max(0, quota.remaining))
  return {
    utilization: (remaining / quota.limit) * 100,
    resetsAt: quota.reset_at || null
  }
}

const grokRequestQuotaBar = computed(() => makeGrokQuotaBar(usageInfo.value?.grok_request_quota))
const grokTokenQuotaBar = computed(() => makeGrokQuotaBar(usageInfo.value?.grok_token_quota))
const grokBilling = computed(() => usageInfo.value?.grok_billing || null)
const grokLocalUsage7d = computed(() => (
  usageInfo.value?.grok_local_usage_7d || usageInfo.value?.seven_day?.window_stats || null
))
const grokLocalUsageMonthly = computed(() => (
  usageInfo.value?.grok_local_usage_monthly || usageInfo.value?.thirty_day?.window_stats || null
))
const grokWeeklyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (billing?.period_type?.toLowerCase() !== 'weekly' || billing.usage_percent == null) {
    return null
  }
  return {
    utilization: Math.min(100, Math.max(0, billing.usage_percent)),
    resetsAt: billing.period_end || null,
    windowStats: grokLocalUsage7d.value
  }
})
// Monthly used/limit % from billing probe (used_percent or derived from cents).
const grokMonthlyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (!billing) return null
  let utilization: number | null = null
  if (billing.used_percent != null && Number.isFinite(billing.used_percent)) {
    utilization = billing.used_percent
  } else if (
    billing.monthly_limit_cents != null &&
    billing.monthly_limit_cents > 0 &&
    billing.used_cents != null
  ) {
    utilization = (billing.used_cents / billing.monthly_limit_cents) * 100
  }
  if (utilization == null) return null
  // Avoid duplicating the weekly bar when period_type is weekly-only without monthly.
  if (billing.period_type?.toLowerCase() === 'weekly' && billing.monthly_limit_cents == null) {
    return null
  }
  return {
    utilization: Math.min(100, Math.max(0, utilization)),
    resetsAt: billing.billing_period_end || billing.period_end || null,
    windowStats: grokLocalUsageMonthly.value
  }
})
const formatGrokMoney = (value?: number | null) => {
  if (value == null || Number.isNaN(value)) return '0'
  if (value >= 1000) return formatCompactNumber(value)
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}
// Prepaid chip only when there is a positive prepaid balance.
// Used/limit only when monthly limit is a positive number (0 means unlimited / unset).
const grokPrepaidMoneyLine = computed(() => {
  const billing = grokBilling.value
  if (!billing) return null
  const prepaid = billing.prepaid_balance
  const showPrepaid = prepaid != null && Number.isFinite(prepaid) && prepaid > 0
  const limitRaw =
    billing.monthly_limit != null
      ? billing.monthly_limit
      : billing.monthly_limit_cents != null
        ? billing.monthly_limit_cents / 100
        : null
  const showUsedLimit = limitRaw != null && Number.isFinite(limitRaw) && limitRaw > 0
  if (!showPrepaid && !showUsedLimit) return null
  const used =
    billing.monthly_used != null
      ? billing.monthly_used
      : billing.used_cents != null
        ? billing.used_cents / 100
        : 0
  return {
    showPrepaid,
    showUsedLimit,
    prepaid: showPrepaid ? formatGrokMoney(prepaid) : null,
    used: showUsedLimit ? formatGrokMoney(used) : null,
    limit: showUsedLimit ? formatGrokMoney(limitRaw) : null
  }
})
const grokPlanLabelIsFree = (value: string) => value.includes('free') || value.includes('basic')
const grokPlanLabelIsPaid = (value: string) => {
  return value !== '' && !grokPlanLabelIsFree(value) && !value.includes('unknown')
}
const grokIsFree = computed(() => {
  if (props.account.platform !== 'grok' || props.account.type !== 'oauth') return false
  const billing = grokBilling.value
  const plan = (billing?.plan || '').trim().toLowerCase()
  const tier = (usageInfo.value?.subscription_tier || '').trim().toLowerCase()
  const entitlement = (usageInfo.value?.grok_entitlement_status || '').toLowerCase()
  if (grokPlanLabelIsFree(tier)) return true
  if (grokPlanLabelIsPaid(tier)) return false
  if (
    billing?.usage_percent != null ||
    billing?.used_percent != null ||
    (billing?.monthly_limit_cents != null && billing.monthly_limit_cents > 0)
  ) return false
  if (grokPlanLabelIsPaid(plan)) return false
  if (
    grokPlanLabelIsFree(plan) ||
    grokPlanLabelIsFree(entitlement)
  ) return true
  return billing != null
})
const grokFreeQuotaUsage = computed(() => usageInfo.value?.grok_local_usage_24h || null)
const grokLocalUsage = computed(() => {
  if (grokIsFree.value) return grokFreeQuotaUsage.value
  return props.todayStats ||
    usageInfo.value?.grok_local_usage ||
    usageInfo.value?.grok_local_usage_7d ||
    usageInfo.value?.grok_local_usage_monthly ||
    null
})
const grokFreeTokenBar = computed(() => {
  if (!grokIsFree.value || !grokFreeQuotaUsage.value) return null
  const limit = usageInfo.value?.grok_free_token_limit
  if (typeof limit !== 'number' || limit <= 0) return null
  const used = Math.max(0, grokFreeQuotaUsage.value.tokens || 0)
  return { utilization: Math.min(100, (used / limit) * 100), limit }
})
const grokQuotaUnknown = computed(() => {
  if (props.account.platform !== 'grok') return false
  if (grokIsFree.value) {
    return !grokFreeTokenBar.value
  }
  if (grokWeeklyBillingBar.value || grokMonthlyBillingBar.value || grokPrepaidMoneyLine.value) {
    return false
  }
  return usageInfo.value?.grok_quota_snapshot_state !== 'observed'
})
const grokQuotaUnknownLabel = computed(() => {
  return usageInfo.value?.grok_quota_snapshot_state === 'no_headers'
    ? t('admin.accounts.usageWindow.grokNoHeaders')
    : t('admin.accounts.usageWindow.grokUnknown')
})
const grokQuotaStatusLine = computed(() => {
  if (props.account.platform !== 'grok') return null
  const parts: string[] = []
  const status = usageInfo.value?.grok_last_status_code
  if (status) {
    parts.push(t('admin.accounts.usageWindow.grokLastStatus', { status }))
  }
  if (usageInfo.value?.grok_last_quota_probe_at) {
    parts.push(t('admin.accounts.usageWindow.grokLastProbe', {
      time: formatRelativeTime(usageInfo.value.grok_last_quota_probe_at)
    }))
  }
  if (usageInfo.value?.grok_last_headers_seen_at) {
    parts.push(t('admin.accounts.usageWindow.grokLastHeadersSeen', {
      time: formatRelativeTime(usageInfo.value.grok_last_headers_seen_at)
    }))
  }
  return parts.length > 0 ? parts.join(' | ') : null
})
const grokRetryAfterLabel = computed(() => {
  const seconds = usageInfo.value?.grok_retry_after_seconds
  if (seconds == null || seconds <= 0) return null
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.ceil(seconds / 60)
  return `${minutes}m`
})

const formatWindowRequests = (stats: WindowStats) => formatCompactNumber(stats.requests, { allowBillions: false })
const formatWindowTokens = (stats: WindowStats) => formatCompactNumber(stats.tokens)
const formatWindowCost = (stats: WindowStats) => stats.cost.toFixed(2)

// 账户类型显示标签
const antigravityTierLabel = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return t('admin.accounts.tier.free')
    case 'g1-pro-tier':
      return t('admin.accounts.tier.pro')
    case 'g1-ultra-tier':
      return t('admin.accounts.tier.ultra')
    default:
      return null
  }
})

// 账户类型徽章样式
const antigravityTierClass = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
    case 'g1-pro-tier':
      return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    case 'g1-ultra-tier':
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    default:
      return ''
  }
})

// 检测账户是否有不合格状态（ineligibleTiers）
const hasIneligibleTiers = computed(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  if (!extra) return false

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return false

  const ineligibleTiers = loadCodeAssist.ineligibleTiers as unknown[] | undefined
  return Array.isArray(ineligibleTiers) && ineligibleTiers.length > 0
})

// Antigravity 403 forbidden 状态
const isForbidden = computed(() => !!usageInfo.value?.is_forbidden)
const forbiddenType = computed(() => usageInfo.value?.forbidden_type || 'forbidden')
const validationURL = computed(() => usageInfo.value?.validation_url || '')

// 需要重新授权（401）
const needsReauth = computed(() => !!usageInfo.value?.needs_reauth)

// 降级错误标签（rate_limited / network_error）
const usageErrorLabel = computed(() => {
  const code = usageInfo.value?.error_code
  if (code === 'rate_limited') return t('admin.accounts.rateLimited')
  return t('admin.accounts.usageError')
})

const forbiddenLabel = computed(() => {
  switch (forbiddenType.value) {
    case 'validation':
      return t('admin.accounts.forbiddenValidation')
    case 'violation':
      return t('admin.accounts.forbiddenViolation')
    default:
      return t('admin.accounts.forbidden')
  }
})

const forbiddenBadgeClass = computed(() => {
  if (forbiddenType.value === 'validation') {
    return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  }
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
})

const linkCopied = ref(false)
const copyValidationURL = async () => {
  if (!validationURL.value) return
  try {
    await navigator.clipboard.writeText(validationURL.value)
    linkCopied.value = true
    setTimeout(() => { linkCopied.value = false }, 2000)
  } catch {
    // fallback: ignore
  }
}

const isAnthropicOAuthOrSetupToken = computed(() => {
  return props.account.platform === 'anthropic' && (props.account.type === 'oauth' || props.account.type === 'setup-token')
})

const applyUsageResult = (result: AccountUsageInfo) => {
  const fetchedAt = Date.now()
  const cached = _usageCache.get(props.account.id)

  if (result.error) {
    error.value = t('admin.accounts.usageFetchFailed')
    if (cached) {
      _usageCache.set(props.account.id, { ...cached, failedAt: fetchedAt })
      usageLastSuccessAt.value = cached.ts
      // Details still expose the latest diagnostic state; list views retain the last good quota.
      usageInfo.value = props.variant === 'detail' ? result : cached.data
    } else {
      usageInfo.value = result
    }
    return
  }

  usageInfo.value = result
  usageLastSuccessAt.value = fetchedAt
  error.value = null
  _usageCache.set(props.account.id, { data: result, ts: fetchedAt })
}

const markUsageRequestFailed = () => {
  const cached = _usageCache.get(props.account.id)
  if (cached) {
    _usageCache.set(props.account.id, { ...cached, failedAt: Date.now() })
  }
  error.value = t('admin.accounts.usageFetchFailed')
}

const requestParentBatchUsage = (options?: { force?: boolean }) => {
  if (!isBatchManaged.value || !shouldFetchUsage.value) return
  props.requestBatchedUsage?.(props.account, options)
}

const syncManagedUsageState = () => {
  if (!isBatchManaged.value) return
  usageInfo.value = props.batchedUsage ?? null
  error.value = props.batchedUsageError ?? null
  loading.value = props.batchedUsageLoading === true
}

const loadUsage = async (options?: {
  source?: 'passive' | 'active'
  bypassCache?: boolean
  force?: boolean
}) => {
  if (!shouldFetchUsage.value) return
  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: options?.force === true || options?.bypassCache === true })
    return
  }

  // An expired value is still useful while a refresh is running or failing.
  const cached = _usageCache.get(props.account.id)
  if (cached) {
    usageInfo.value = cached.data
    usageLastSuccessAt.value = cached.ts
    error.value = cached.failedAt ? t('admin.accounts.usageFetchFailed') : null
    if (!options?.bypassCache && Date.now() - cached.ts < USAGE_CACHE_TTL) {
      loading.value = false
      return
    }
  }

  loading.value = true
  error.value = null

  try {
    const result = await requestUsage(props.account, options?.source, options?.force)
    if (!unmounted.value) {
      applyUsageResult(result)
    }
  } catch (e: any) {
    if (!unmounted.value) {
      markUsageRequestFailed()
      console.error('Failed to load usage:', e)
    }
  } finally {
    if (!unmounted.value) loading.value = false
  }
}

const flushPendingAutoLoad = () => {
  if (!pendingAutoLoad.value) return
  const source = pendingAutoLoadSource.value
  pendingAutoLoad.value = false
  pendingAutoLoadSource.value = undefined
  loadUsage({ source }).catch((e) => {
    console.error('Failed to load deferred usage:', e)
  })
}

const requestAutoLoad = (source?: 'passive' | 'active') => {
  if (!shouldFetchUsage.value) return
  if (shouldLazyLoadOnMobile.value && !hasEnteredViewport.value) {
    pendingAutoLoad.value = true
    pendingAutoLoadSource.value = source
    return
  }
  loadUsage({ source }).catch((e) => {
    console.error('Failed to auto load usage:', e)
  })
}

const detachVisibilityObserver = () => {
  visibilityObserver?.disconnect()
  visibilityObserver = null
}

const attachVisibilityObserver = () => {
  detachVisibilityObserver()
  if (!shouldLazyLoadOnMobile.value || hasEnteredViewport.value) return
  if (typeof window === 'undefined' || typeof IntersectionObserver === 'undefined') {
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  if (!rootRef.value) return

  visibilityObserver = new IntersectionObserver((entries) => {
    if (!entries.some((entry) => entry.isIntersecting)) return
    hasEnteredViewport.value = true
    detachVisibilityObserver()
    flushPendingAutoLoad()
  }, {
    root: null,
    rootMargin: '200px 0px',
    threshold: 0.01
  })
  visibilityObserver.observe(rootRef.value)
}

const loadActiveUsage = async () => {
  activeQueryLoading.value = true
  try {
    const result = await requestUsage(props.account, 'active', true)
    applyUsageResult(result)
  } catch (e: any) {
    markUsageRequestFailed()
    console.error('Failed to load active usage:', e)
  } finally {
    activeQueryLoading.value = false
  }
}

const handleGrokProbed = (result: GrokQuotaProbeResult) => {
  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: true })
    return
  }
  const current = usageInfo.value
  if (!current) return
  const snapshot = result.snapshot
  const statusCode = snapshot?.status_code ?? result.status_code
  const hasActiveProbeSnapshot = snapshot != null && (
    result.source === 'active_probe' ||
    result.source === 'hybrid_probe' ||
    snapshot.observation_source === 'active_probe'
  )
  const probeSucceeded = hasActiveProbeSnapshot &&
    statusCode != null && statusCode >= 200 && statusCode < 300
  const snapshotEntitlement = snapshot?.entitlement_status?.trim()
  const currentEntitlement = current.grok_entitlement_status?.trim()
  const entitlementStatus = snapshotEntitlement || (
    probeSucceeded && currentEntitlement?.toLowerCase() === 'forbidden'
      ? undefined
      : current.grok_entitlement_status
  )
  const merged: AccountUsageInfo = {
    ...current,
    grok_billing: result.billing ?? current.grok_billing,
    grok_local_usage_24h: result.local_usage_24h ?? current.grok_local_usage_24h,
    grok_local_usage_7d: result.local_usage_7d ?? current.grok_local_usage_7d,
    grok_local_usage_monthly: result.local_usage_monthly ?? current.grok_local_usage_monthly,
    grok_request_quota: snapshot?.requests ?? current.grok_request_quota,
    grok_token_quota: snapshot?.tokens ?? current.grok_token_quota,
    grok_retry_after_seconds: snapshot?.retry_after_seconds ?? current.grok_retry_after_seconds,
    grok_entitlement_status: entitlementStatus,
    grok_quota_snapshot_state: result.billing
      ? 'billing_observed'
      : snapshot?.headers_observed
        ? 'observed'
        : current.grok_quota_snapshot_state,
    grok_last_quota_probe_at: result.billing?.fetched_at ?? snapshot?.last_probe_at ?? current.grok_last_quota_probe_at,
    grok_last_headers_seen_at: snapshot?.last_headers_seen_at ?? current.grok_last_headers_seen_at,
    grok_last_status_code: result.status_code ?? snapshot?.status_code ?? current.grok_last_status_code,
    is_forbidden: probeSucceeded ? false : current.is_forbidden,
    forbidden_reason: probeSucceeded ? undefined : current.forbidden_reason,
    forbidden_type: probeSucceeded ? undefined : current.forbidden_type,
    validation_url: probeSucceeded ? undefined : current.validation_url,
    needs_verify: probeSucceeded ? false : current.needs_verify,
    is_banned: probeSucceeded ? false : current.is_banned,
    error: result.billing || snapshot ? undefined : current.error,
    error_code: result.billing || snapshot ? undefined : current.error_code
  }
  usageInfo.value = merged
  const fetchedAt = Date.now()
  usageLastSuccessAt.value = fetchedAt
  error.value = null
  _usageCache.set(props.account.id, { data: merged, ts: fetchedAt })
}

// ===== API Key quota progress bars =====

interface QuotaBarInfo {
  utilization: number
  resetsAt: string | null
}

const makeQuotaBar = (
  used: number,
  limit: number,
  startKey?: string
): QuotaBarInfo => {
  const utilization = limit > 0 ? (used / limit) * 100 : 0
  let resetsAt: string | null = null
  if (startKey) {
    const extra = props.account.extra as Record<string, unknown> | undefined
    const isDaily = startKey.includes('daily')
    const mode = isDaily
      ? (extra?.quota_daily_reset_mode as string) || 'rolling'
      : (extra?.quota_weekly_reset_mode as string) || 'rolling'

    if (mode === 'fixed') {
      // Use pre-computed next reset time for fixed mode
      const resetAtKey = isDaily ? 'quota_daily_reset_at' : 'quota_weekly_reset_at'
      resetsAt = (extra?.[resetAtKey] as string) || null
    } else {
      // Rolling mode: compute from start + period
      const startStr = extra?.[startKey] as string | undefined
      if (startStr) {
        const startDate = new Date(startStr)
        const periodMs = isDaily ? 24 * 60 * 60 * 1000 : 7 * 24 * 60 * 60 * 1000
        resetsAt = new Date(startDate.getTime() + periodMs).toISOString()
      }
    }
  }
  return { utilization, resetsAt }
}

const hasApiKeyQuota = computed(() => {
  if (props.account.type !== 'apikey' && props.account.type !== 'bedrock') return false
  return (
    (props.account.quota_daily_limit ?? 0) > 0 ||
    (props.account.quota_weekly_limit ?? 0) > 0 ||
    (props.account.quota_limit ?? 0) > 0
  )
})

const quotaDailyBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_daily_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_daily_used ?? 0, limit, 'quota_daily_start')
})

const quotaWeeklyBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_weekly_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_weekly_used ?? 0, limit, 'quota_weekly_start')
})

const quotaTotalBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_used ?? 0, limit)
})

interface CompactUsageSegment {
  key: string
  label: string
  utilization: number
  resetsAt?: string | null
  remainingCapacity?: boolean
  color: 'indigo' | 'emerald' | 'purple' | 'amber'
}

const parseObservedAt = (value: unknown): number | null => {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value !== 'string' || !value.trim()) return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

const hasOllamaUsageData = computed(() => {
  const data = props.account.ollama_cloud_usage?.snapshot?.data
  return Boolean(data?.five_hour || data?.seven_day)
})

const hasRemoteUsageData = computed(() => {
  const info = usageInfo.value
  if (!info) return false
  return Boolean(
    info.five_hour ||
    info.seven_day ||
    info.seven_day_sonnet ||
    info.seven_day_fable ||
    info.gemini_shared_daily ||
    info.gemini_pro_daily ||
    info.gemini_flash_daily ||
    info.gemini_shared_minute ||
    info.gemini_pro_minute ||
    info.gemini_flash_minute ||
    (info.antigravity_quota && Object.keys(info.antigravity_quota).length > 0) ||
    info.grok_billing ||
    info.grok_request_quota ||
    info.grok_token_quota ||
    info.grok_local_usage ||
    info.grok_local_usage_24h ||
    info.grok_local_usage_7d ||
    info.grok_local_usage_monthly ||
    (info.ai_credits && info.ai_credits.length > 0)
  )
})

const hasDisplayUsageData = computed(() => Boolean(
  hasRemoteUsageData.value ||
  hasOllamaUsageData.value ||
  props.todayStats ||
  hasApiKeyQuota.value
))

const usageObservedAt = computed(() => {
  const info = usageInfo.value
  const candidates = [
    info?.updated_at,
    info?.grok_billing?.updated_at,
    info?.grok_billing?.fetched_at,
    info?.grok_last_headers_seen_at,
    info?.grok_last_quota_probe_at,
    props.account.ollama_cloud_usage?.snapshot?.last_attempt_at
  ]
  const observed = candidates
    .map(parseObservedAt)
    .filter((timestamp): timestamp is number => timestamp != null)
  if (observed.length > 0) return Math.max(...observed)
  return usageLastSuccessAt.value
})

const usageResponseFailed = computed(() => Boolean(error.value || usageInfo.value?.error))

const usageIsStale = computed(() => {
  if (!hasRemoteUsageData.value && !hasOllamaUsageData.value) return false
  if (usageResponseFailed.value) return true
  const observedAt = usageObservedAt.value
  return observedAt != null && props.statusNow - observedAt > USAGE_STALE_AFTER
})

const todayStatsAreStale = computed(() => {
  if (!props.todayStats) return false
  if (props.todayStatsError) return true
  return props.todayStatsUpdatedAt != null &&
    props.statusNow - props.todayStatsUpdatedAt > USAGE_STALE_AFTER
})

const showStaleMarker = computed(() => usageIsStale.value || todayStatsAreStale.value)

const compactUsageSegments = computed<CompactUsageSegment[]>(() => {
  const segments: CompactUsageSegment[] = []
  const pushWindow = (
    key: string,
    label: string,
    window: { utilization: number; resets_at?: string | null } | null | undefined,
    color: CompactUsageSegment['color']
  ) => {
    if (!window) return
    segments.push({ key, label, utilization: window.utilization, resetsAt: window.resets_at, color })
  }

  if (props.account.platform === 'anthropic' || props.account.platform === 'openai') {
    pushWindow('five-hour', '5h', usageInfo.value?.five_hour, 'indigo')
    pushWindow('seven-day', '7d', usageInfo.value?.seven_day, 'emerald')
    pushWindow('seven-day-sonnet', '7d S', usageInfo.value?.seven_day_sonnet, 'purple')
    pushWindow('seven-day-fable', '7d F', usageInfo.value?.seven_day_fable, 'amber')
  } else if (props.account.platform === 'antigravity') {
    const antigravityBars = [
      ['ag-pro', t('admin.accounts.usageWindow.gemini3Pro'), antigravity3ProUsageFromAPI.value, 'indigo'],
      ['ag-flash', t('admin.accounts.usageWindow.gemini3Flash'), antigravity3FlashUsageFromAPI.value, 'emerald'],
      ['ag-image', t('admin.accounts.usageWindow.gemini3Image'), antigravity3ImageUsageFromAPI.value, 'purple'],
      ['ag-claude', t('admin.accounts.usageWindow.claude'), antigravityClaudeUsageFromAPI.value, 'amber']
    ] as const
    for (const [key, label, value, color] of antigravityBars) {
      if (value) segments.push({ key, label, utilization: value.utilization, resetsAt: value.resetTime, color })
    }
  } else if (props.account.platform === 'grok') {
    if (grokWeeklyBillingBar.value) {
      segments.push({ key: 'grok-weekly', label: '7d', ...grokWeeklyBillingBar.value, color: 'indigo' })
    } else if (grokFreeTokenBar.value) {
      segments.push({ key: 'grok-free', label: '24h', utilization: grokFreeTokenBar.value.utilization, color: 'emerald' })
    } else {
      if (grokRequestQuotaBar.value) {
        segments.push({
          key: 'grok-requests',
          label: t('admin.accounts.usageWindow.grokRequests'),
          ...grokRequestQuotaBar.value,
          remainingCapacity: true,
          color: 'indigo'
        })
      }
      if (grokTokenQuotaBar.value) {
        segments.push({
          key: 'grok-tokens',
          label: t('admin.accounts.usageWindow.grokTokens'),
          ...grokTokenQuotaBar.value,
          remainingCapacity: true,
          color: 'emerald'
        })
      }
    }
  } else if (props.account.platform === 'gemini') {
    for (const bar of geminiUsageBars.value) {
      segments.push({
        key: `gemini-${bar.key}`,
        label: bar.label,
        utilization: bar.utilization,
        resetsAt: bar.resetsAt,
        color: bar.color
      })
    }
  }

  const ollamaData = props.account.ollama_cloud_usage?.snapshot?.data
  if (ollamaData?.five_hour) {
    segments.push({
      key: 'ollama-five-hour',
      label: '5h',
      utilization: ollamaData.five_hour.used_percent,
      resetsAt: ollamaData.five_hour.reset_at,
      color: 'indigo'
    })
  }
  if (ollamaData?.seven_day) {
    segments.push({
      key: 'ollama-seven-day',
      label: '7d',
      utilization: ollamaData.seven_day.used_percent,
      resetsAt: ollamaData.seven_day.reset_at,
      color: 'emerald'
    })
  }

  if (quotaDailyBar.value) {
    segments.push({ key: 'quota-daily', label: '1d', ...quotaDailyBar.value, color: 'indigo' })
  }
  if (quotaWeeklyBar.value) {
    segments.push({ key: 'quota-weekly', label: '7d', ...quotaWeeklyBar.value, color: 'emerald' })
  }
  if (quotaTotalBar.value) {
    segments.push({ key: 'quota-total', label: 'total', ...quotaTotalBar.value, color: 'purple' })
  }
  return segments
})

const shouldSummarizeTodayStats = computed(() => (
  props.account.type === 'apikey' ||
  props.account.type === 'bedrock' ||
  props.account.type === 'service_account'
))

const compactTextSummary = computed(() => {
  if (props.todayStats && shouldSummarizeTodayStats.value) {
    const parts = [
      `${formatCompactNumber(props.todayStats.requests, { allowBillions: false })} req`,
      `${formatCompactNumber(props.todayStats.tokens)} Token`,
      `A $${props.todayStats.cost.toFixed(2)}`
    ]
    if (props.todayStats.user_cost != null) parts.push(`U $${props.todayStats.user_cost.toFixed(2)}`)
    return parts.join(' · ')
  }
  if (aiCreditsDisplay.value && compactUsageSegments.value.length === 0) {
    return `${t('admin.accounts.aiCreditsBalance')}: ${aiCreditsDisplay.value}`
  }
  if (props.account.platform === 'grok' && grokLocalUsage.value && compactUsageSegments.value.length === 0) {
    return `${formatWindowRequests(grokLocalUsage.value)} req · ${formatWindowTokens(grokLocalUsage.value)} · A $${formatWindowCost(grokLocalUsage.value)}`
  }
  if (
    props.account.platform === 'gemini' &&
    !loading.value &&
    !usageResponseFailed.value &&
    compactUsageSegments.value.length === 0
  ) {
    return t('admin.accounts.gemini.rateLimit.unlimited')
  }
  return ''
})

const compactHasData = computed(() => Boolean(
  compactTextSummary.value || compactUsageSegments.value.length > 0
))

const compactLoading = computed(() => {
  if (compactHasData.value) return false
  if (loading.value && shouldFetchUsage.value) return true
  return props.todayStatsLoading && shouldSummarizeTodayStats.value
})

const compactPartialFailure = computed(() => {
  if (!hasDisplayUsageData.value) return false
  return Boolean(
    (usageResponseFailed.value && !hasRemoteUsageData.value) ||
    (props.todayStatsError && !props.todayStats && shouldSummarizeTodayStats.value)
  )
})

const hasFetchFailureWithoutData = computed(() => Boolean(
  !hasDisplayUsageData.value &&
  (usageResponseFailed.value || (props.todayStatsError && shouldSummarizeTodayStats.value))
))

const emptyUsageText = computed(() => {
  if (props.variant === 'detail') return '-'
  return hasFetchFailureWithoutData.value
    ? t('admin.accounts.usageFetchFailed')
    : t('admin.accounts.usageNoData')
})

const handleQuotaResetAccountUpdated = (account: Account) => {
  // The reset response already carries authoritative quota and account data.
  // Avoid turning the parent patch into a second automatic /usage request.
  // The suppression is time-boxed so an unhandled emit (parent that ignores
  // account-updated) cannot latch it and swallow a later, unrelated refresh.
  suppressOpenAIUsageRefreshUntil.value = Date.now() + SUPPRESS_USAGE_REFRESH_WINDOW_MS
  emit('account-updated', account)
}
const handleOllamaCloudUsageUpdated = (state: NonNullable<Account['ollama_cloud_usage']>) => {
  emit('account-updated', { ...props.account, ollama_cloud_usage: state })
}

// ===== Key account today stats formatters =====

const formatKeyRequests = computed(() => {
  if (!props.todayStats) return ''
  return formatCompactNumber(props.todayStats.requests, { allowBillions: false })
})

const formatKeyTokens = computed(() => {
  if (!props.todayStats) return ''
  return formatCompactNumber(props.todayStats.tokens)
})

const formatKeyCost = computed(() => {
  if (!props.todayStats) return '0.00'
  return props.todayStats.cost.toFixed(2)
})

const formatKeyUserCost = computed(() => {
  if (!props.todayStats || props.todayStats.user_cost == null) return '0.00'
  return props.todayStats.user_cost.toFixed(2)
})

onMounted(() => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(desktopViewportQuery)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }

  if (isBatchManaged.value) {
    syncManagedUsageState()
    requestParentBatchUsage()
    return
  }

  if (!shouldAutoLoadUsageOnMount.value) return
  const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
  requestAutoLoad(source)
})

watch(
  () => [props.batchedUsage, props.batchedUsageError, props.batchedUsageLoading, isBatchManaged.value] as const,
  () => {
    syncManagedUsageState()
  },
  { immediate: true, deep: true }
)

watch(isBatchManaged, (managed, wasManaged) => {
  if (managed && !wasManaged) {
    syncManagedUsageState()
    requestParentBatchUsage()
  }
})

watch(
  () => [props.account.id, props.account.platform, props.account.type, isBatchManaged.value] as const,
  ([accountID, platform, accountType, managed], [previousAccountID, previousPlatform, previousAccountType]) => {
    if (
      accountID === previousAccountID &&
      platform === previousPlatform &&
      accountType === previousAccountType
    ) {
      return
    }
    if (!managed || !shouldFetchUsage.value) return
    syncManagedUsageState()
    requestParentBatchUsage()
  },
  { flush: 'post' }
)

watch(openAIUsageRefreshKey, (nextKey, prevKey) => {
  if (!prevKey || nextKey === prevKey) return
  if (props.account.platform !== 'openai' || props.account.type !== 'oauth') return
  if (Date.now() < suppressOpenAIUsageRefreshUntil.value) {
    suppressOpenAIUsageRefreshUntil.value = 0
    return
  }

  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: true })
    return
  }

  // Incremental list refreshes may update last_used_at every few seconds; keep the 5-minute cache.
  requestAutoLoad()
})

watch(
  () => props.manualRefreshToken,
  (nextToken, prevToken) => {
    if (nextToken === prevToken) return
    if (!shouldFetchUsage.value) return

    if (isBatchManaged.value) {
      requestParentBatchUsage({ force: true })
      return
    }

    const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
    loadUsage({ source, bypassCache: true, force: true }).catch((e) => {
      console.error('Failed to refresh usage after manual refresh:', e)
    })
  }
)

watch(
  [rootRef, shouldLazyLoadOnMobile],
  () => {
    if (shouldLazyLoadOnMobile.value) {
      attachVisibilityObserver()
      return
    }
    detachVisibilityObserver()
  },
  { immediate: true, flush: 'post' }
)

watch(isDesktopViewport, (isDesktop) => {
  if (isDesktop) {
    detachVisibilityObserver()
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  hasEnteredViewport.value = false
  attachVisibilityObserver()
})

onUnmounted(() => {
  detachVisibilityObserver()
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
  }
  desktopViewportListener = null
  desktopViewportMediaQuery = null
})
</script>
