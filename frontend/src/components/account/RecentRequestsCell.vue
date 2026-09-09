<template>
  <div class="min-w-[116px]">
    <div v-if="!requests.length && loading" class="flex h-7 items-center gap-1" aria-label="Loading recent requests">
      <span v-for="index in 10" :key="index" class="h-6 w-1.5 animate-pulse rounded-full bg-gray-200 dark:bg-dark-600" />
    </div>
    <div v-else-if="requests.length" :class="['transition-opacity duration-200', loading ? 'opacity-60' : 'opacity-100']" :aria-label="t('admin.accounts.recentRequests.summary', { count: requests.length })">
      <div class="mb-1 text-[11px] font-medium tabular-nums text-gray-500 dark:text-gray-400">
        {{ formatTime(requests[0].created_at) }}
      </div>
      <div class="flex h-6 items-center gap-1">
      <template v-for="(request, index) in requests" :key="`${request.request_id}-${index}`">
        <HelpTooltip class="!ml-0" :content="requestTooltip(request)" width-class="w-80">
          <template #trigger>
            <span :class="request.kind === 'error'
              ? 'block h-6 w-1.5 cursor-help rounded-full bg-red-500 shadow-sm shadow-red-200 dark:bg-red-400 dark:shadow-none'
              : 'block h-6 w-1.5 cursor-help rounded-full bg-emerald-500 shadow-sm shadow-emerald-200 dark:bg-emerald-400 dark:shadow-none'" />
          </template>
        </HelpTooltip>
      </template>
      </div>
    </div>
    <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{ t('admin.accounts.recentRequests.empty') }}</span>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { OpsRequestDetail } from '@/api/admin/ops'

defineProps<{
  requests: OpsRequestDetail[]
  loading?: boolean
}>()

const { t } = useI18n()

const formatTime = (value: string) => {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value
  return new Intl.DateTimeFormat(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(timestamp)
}

const requestTooltip = (request: OpsRequestDetail) => {
  const status = request.status_code || (request.kind === 'error' ? 500 : 200)
  const latency = request.duration_ms == null ? '-' : `${request.duration_ms} ms`
  const reason = request.message?.trim() || t('admin.accounts.recentRequests.unknownError')
  const lines = [
    `${formatTime(request.created_at)} · ${status}`,
    `用户: ${request.user_id ?? '-'}`,
    `分组: ${request.group_id ?? '-'}`,
    `延迟: ${latency}`,
  ]
  if (request.kind === 'error') lines.push(`${t('admin.accounts.recentRequests.errorPrefix', { status })}: ${reason}`)
  if (request.phase) lines.push(request.phase)
  return lines.join('\n')
}
</script>
