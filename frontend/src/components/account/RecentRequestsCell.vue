<template>
  <div class="min-w-[116px]">
    <div v-if="!requests.length && loading" class="flex h-7 items-center justify-end gap-1" aria-label="Loading recent requests">
      <span v-for="index in 10" :key="index" class="h-6 w-1.5 animate-pulse rounded-full bg-gray-200 dark:bg-dark-600" />
    </div>
    <div v-else-if="timeline.length" :class="['transition-opacity duration-200', loading ? 'opacity-60' : 'opacity-100']" :aria-label="t('admin.accounts.recentRequests.summary', { count: timeline.length })">
      <div class="mb-1 text-right text-[11px] font-medium tabular-nums text-gray-500 dark:text-gray-400">
        {{ formatTime(latestCreatedAt) }}
      </div>
      <div class="flex h-6 items-center justify-end gap-1">
      <template v-for="request in timeline" :key="request.request_id">
        <HelpTooltip class="!ml-0" trigger="both" width-class="w-80">
          <template #trigger>
            <span :class="request.kind === 'error'
              ? 'block h-6 w-1.5 cursor-help rounded-full bg-red-500 shadow-sm shadow-red-200 dark:bg-red-400 dark:shadow-none'
              : 'block h-6 w-1.5 cursor-help rounded-full bg-emerald-500 shadow-sm shadow-emerald-200 dark:bg-emerald-400 dark:shadow-none'" />
          </template>
          <div class="space-y-1 text-left">
            <div class="font-semibold">{{ request.kind === 'error' ? '错误请求' : '成功请求' }}</div>
            <div>时间：{{ formatTime(request.created_at) }}</div>
            <div>用户：{{ request.user_id ?? '-' }}</div>
            <div>分组：{{ request.group_id ?? '-' }}</div>
            <div>延迟：{{ request.duration_ms == null ? '-' : `${request.duration_ms} ms` }}</div>
            <div>状态：{{ request.status_code || (request.kind === 'error' ? 500 : 200) }}</div>
            <div v-if="request.kind === 'error'" class="whitespace-pre-wrap text-red-200">原因：{{ request.message || '未知错误' }}</div>
          </div>
        </HelpTooltip>
      </template>
      </div>
    </div>
    <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{ t('admin.accounts.recentRequests.empty') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { OpsRequestDetail } from '@/api/admin/ops'

const { t } = useI18n()

const props = defineProps<{
  requests: OpsRequestDetail[]
  loading?: boolean
}>()

// API returns created_at_desc (newest first). Reverse so the sparkline reads
// oldest on the left and newest on the right, matching a timeline.
const timeline = computed(() => [...props.requests].reverse())

const latestCreatedAt = computed(() => timeline.value[timeline.value.length - 1]?.created_at ?? '')

const formatTime = (value: string) => {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value
  return new Intl.DateTimeFormat(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(timestamp)
}

</script>
