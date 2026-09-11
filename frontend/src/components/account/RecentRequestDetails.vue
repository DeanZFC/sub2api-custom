<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpsRequestDetail } from '@/api/admin/ops'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { formatCacheHitRate } from '@/utils/cacheHitRate'
import { resolveUsageRequestType } from '@/utils/usageRequestType'

const props = defineProps<{ request: OpsRequestDetail }>()
const { t } = useI18n()
const number = (value?: number | null) => value == null ? '—' : value.toLocaleString()
const money = (value?: number | null) => value == null ? '—' : `$${value.toFixed(6)}`
const duration = (ms?: number | null) => {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)}s`
  const seconds = Math.round(ms / 1000)
  return seconds < 3600
    ? `${Math.floor(seconds / 60)}m ${seconds % 60}s`
    : `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}
const requestTypeLabel = computed(() => {
  switch (resolveUsageRequestType(props.request)) {
    case 'stream': return t('usage.stream')
    case 'sync': return t('usage.sync')
    case 'ws_v2': return 'WS v2'
    case 'cyber': return t('usage.cyber')
    case 'live': return t('usage.live')
    default: return t('usage.unknown')
  }
})
const cacheHitRate = computed(() => {
  const r = props.request
  if (r.input_tokens == null || r.cache_read_tokens == null || r.cache_creation_tokens == null) return '—'
  return formatCacheHitRate(Math.max(0, r.input_tokens - (r.image_input_tokens ?? 0)), r.cache_read_tokens, r.cache_creation_tokens, 2)
})
</script>

<template>
  <div class="space-y-2 text-xs leading-5">
    <div v-if="request.kind === 'error'" class="rounded-md border border-red-400/20 bg-red-400/10 px-2.5 py-2 text-red-200">
      <div class="mb-0.5 font-medium">{{ t('admin.accounts.recentRequests.reason') }}</div>
      <p class="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{{ request.message || t('admin.accounts.recentRequests.unknownError') }}</p>
    </div>
    <dl class="request-details-grid">
      <dt>{{ t('usage.time') }}</dt>
      <dd class="tabular-nums">{{ formatDateTime(request.created_at) }}</dd>
      <dt>{{ t('admin.accounts.recentRequests.user') }}</dt>
      <dd data-testid="recent-request-user">
        <span class="text-sky-300">{{ request.user_email || '—' }}</span>
        <span v-if="request.user_id != null" class="ml-1 text-gray-400">#{{ request.user_id }}</span>
      </dd>
      <template v-if="request.account_name">
        <dt>{{ t('admin.accounts.recentRequests.account') }}</dt><dd>{{ request.account_name }}</dd>
      </template>
      <dt>{{ t('usage.model') }}</dt>
      <dd>
        <span class="font-medium">{{ request.model || '—' }}</span>
        <div v-if="request.upstream_model && request.upstream_model !== request.model" class="text-gray-400">↳ {{ request.upstream_model }}</div>
      </dd>
      <dt>{{ t('usage.type') }}</dt>
      <dd><span class="inline-block rounded bg-blue-400/20 px-2 py-0.5 font-medium text-blue-200">{{ requestTypeLabel }}</span></dd>
    </dl>

    <dl class="request-details-grid border-t border-white/10 pt-2 tabular-nums">
      <dt>{{ t('usage.tokens') }}</dt>
      <dd class="flex flex-wrap gap-x-4 gap-y-1" data-testid="recent-request-tokens">
        <span class="inline-flex items-center gap-1" :title="t('usage.in')"><Icon name="arrowDown" size="sm" class="text-emerald-400" />{{ number(request.input_tokens) }}</span>
        <span class="inline-flex items-center gap-1" :title="t('usage.out')"><Icon name="arrowUp" size="sm" class="text-violet-400" />{{ number(request.output_tokens) }}</span>
      </dd>
      <dt>{{ t('usage.cacheReadTokensLabel') }}</dt><dd class="text-sky-300">{{ number(request.cache_read_tokens) }}</dd>
      <dt>{{ t('usage.cacheCreationTokensLabel') }}</dt><dd class="text-amber-300">{{ number(request.cache_creation_tokens) }}</dd>
      <dt>{{ t('usage.cacheHitRate') }}</dt><dd data-testid="recent-request-cache-rate" class="text-sky-300">{{ cacheHitRate }}</dd>
      <template v-if="request.image_input_tokens">
        <dt>{{ t('usage.imageInputTokens') }}</dt><dd class="text-fuchsia-300">{{ number(request.image_input_tokens) }}</dd>
      </template>
      <template v-if="request.image_output_tokens">
        <dt>{{ t('usage.imageOutputTokens') }}</dt><dd class="text-pink-300">{{ number(request.image_output_tokens) }}</dd>
      </template>
    </dl>

    <dl class="request-details-grid border-t border-white/10 pt-2 tabular-nums">
      <dt>{{ t('usage.userBilled') }}</dt><dd class="text-emerald-300">{{ money(request.actual_cost) }}</dd>
      <dt>{{ t('usage.accountBilled') }}</dt><dd class="text-orange-300">{{ money(request.account_cost) }}</dd>
      <dt>{{ t('usage.latencyFirstToken') }}</dt><dd>{{ duration(request.first_token_ms) }}</dd>
      <dt>{{ t('usage.latencyDuration') }}</dt><dd>{{ duration(request.duration_ms) }}</dd>
    </dl>
    <div v-if="request.request_id" class="border-t border-white/10 pt-1.5 text-gray-400">
      <span>{{ t('admin.accounts.recentRequests.requestId') }}</span>
      <div class="break-all font-mono text-[11px]">{{ request.request_id }}</div>
    </div>
  </div>
</template>

<style scoped>
.request-details-grid {
  display: grid;
  grid-template-columns: 4.75rem minmax(0, 1fr);
  gap: 0.25rem 0.625rem;
}
.request-details-grid dt { color: #9ca3af; }
.request-details-grid dd { min-width: 0; overflow-wrap: anywhere; }
</style>
