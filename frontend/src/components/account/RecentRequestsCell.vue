<template>
  <div class="min-w-[116px]">
    <div v-if="!requests.length && loading" class="flex h-7 items-center justify-end gap-1" :aria-label="t('common.loading')">
      <span v-for="index in 10" :key="index" class="h-6 w-1.5 animate-pulse rounded-full bg-gray-200 dark:bg-dark-600" />
    </div>
    <div v-else-if="timeline.length" :aria-label="t('admin.accounts.recentRequests.summary', { count: timeline.length })">
      <div class="mb-1 text-right text-[11px] font-medium tabular-nums text-gray-500 dark:text-gray-400">{{ formatTime(latestCreatedAt) }}</div>
      <div class="flex h-6 items-center justify-end gap-1">
        <button
          v-for="request in timeline"
          :key="requestKey(request)"
          type="button"
          class="block h-6 w-1.5 shrink-0 rounded-full shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-primary-400 focus-visible:ring-offset-2"
          :class="request.kind === 'error' ? 'bg-red-500 shadow-red-200 dark:bg-red-400 dark:shadow-none' : 'bg-emerald-500 shadow-emerald-200 dark:bg-emerald-400 dark:shadow-none'"
          :aria-label="t('admin.accounts.recentRequests.viewDetails', { time: formatTime(request.created_at) })"
          :aria-expanded="activeKey === requestKey(request)"
          aria-haspopup="dialog"
          data-testid="recent-request-trigger"
          @mouseenter="showRequest(request, $event)"
          @mouseleave="scheduleClose"
          @focus="showRequest(request, $event)"
          @blur="scheduleClose"
          @click.stop="togglePinned(request, $event)"
        />
      </div>
    </div>
    <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{ t('admin.accounts.recentRequests.empty') }}</span>

    <Teleport to="body">
      <div
        v-if="activeRequest"
        ref="panel"
        role="dialog"
        :aria-label="activeRequest.kind === 'error' ? t('admin.accounts.recentRequests.error') : t('admin.accounts.recentRequests.success')"
        class="fixed z-[99999] flex flex-col overflow-hidden rounded-xl bg-gray-900 text-left text-white shadow-xl ring-1 ring-white/10 selection:bg-primary-200 selection:text-gray-900 dark:bg-gray-800"
        :style="panelStyle"
        @mouseenter="cancelClose"
        @mouseleave="scheduleClose"
        @focusin="cancelClose"
        @focusout="scheduleClose"
      >
        <div class="flex shrink-0 items-center gap-2 border-b border-white/10 px-4 py-3">
          <span class="h-2 w-2 rounded-full" :class="activeRequest.kind === 'error' ? 'bg-red-400' : 'bg-emerald-400'" />
          <span class="text-sm font-semibold">{{ activeRequest.kind === 'error' ? t('admin.accounts.recentRequests.error') : t('admin.accounts.recentRequests.success') }}</span>
          <span class="rounded bg-white/10 px-1.5 py-0.5 font-mono text-[11px] text-gray-300">{{ activeRequest.status_code ?? (activeRequest.kind === 'success' ? 200 : '—') }}</span>
          <button type="button" class="ml-auto rounded p-1 text-gray-400 hover:bg-white/10 hover:text-white" :aria-label="t('common.close')" @click.stop="close">
            <Icon name="x" size="sm" />
          </button>
        </div>
        <div class="min-h-0 overflow-y-auto overscroll-contain p-4">
          <RecentRequestDetails :request="activeRequest" />
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch, type CSSProperties } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpsRequestDetail } from '@/api/admin/ops'
import Icon from '@/components/icons/Icon.vue'
import RecentRequestDetails from './RecentRequestDetails.vue'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const props = defineProps<{ requests: OpsRequestDetail[]; loading?: boolean }>()

// The API sends newest first; keep newest at the right of the timeline.
const timeline = computed(() => [...props.requests].reverse())
const latestCreatedAt = computed(() => props.requests[0]?.created_at ?? '')
const formatTime = (value: string) => formatDateTime(value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const requestKey = (request: OpsRequestDetail) => `${request.kind}:${request.error_id ?? request.request_id}:${request.created_at}`
const activeRequest = shallowRef<OpsRequestDetail | null>(null)
const activeKey = computed(() => activeRequest.value ? requestKey(activeRequest.value) : null)
const panel = ref<HTMLElement | null>(null)
const panelStyle = ref<CSSProperties>({})
let anchor: HTMLElement | null = null
let pinned = false
let closeTimer: ReturnType<typeof setTimeout> | undefined

function cancelClose() {
  clearTimeout(closeTimer)
  closeTimer = undefined
}

function updatePosition() {
  if (!anchor?.isConnected) return
  const position = getFloatingPanelPosition(anchor.getBoundingClientRect(), window.innerWidth, window.innerHeight, { maxWidth: 360, minComfortableHeight: 480 })
  panelStyle.value = {
    top: position.top == null ? undefined : `${position.top}px`,
    bottom: position.bottom == null ? undefined : `${position.bottom}px`,
    left: `${position.left}px`,
    width: `${position.width}px`,
    maxHeight: `${position.maxHeight}px`,
  }
}

function showRequest(request: OpsRequestDetail, event: Event) {
  if (pinned) return
  cancelClose()
  anchor = event.currentTarget as HTMLElement
  activeRequest.value = request
  updatePosition()
}

function togglePinned(request: OpsRequestDetail, event: Event) {
  if (pinned && activeKey.value === requestKey(request)) {
    close()
    return
  }
  pinned = false
  showRequest(request, event)
  pinned = true
}

function close() {
  cancelClose()
  activeRequest.value = null
  anchor = null
  pinned = false
}

function scheduleClose() {
  if (pinned) return
  cancelClose()
  closeTimer = setTimeout(close, 150)
}

function onOutsideClick(event: MouseEvent) {
  if (!(event.target instanceof Node)) return
  if (anchor?.contains(event.target) || panel.value?.contains(event.target)) return
  close()
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') close()
}

// Only an open panel needs global listeners or a detail subtree.
watch(() => activeRequest.value !== null, (open, _, onCleanup) => {
  if (!open) return
  document.addEventListener('click', onOutsideClick, true)
  document.addEventListener('keydown', onKeydown)
  window.addEventListener('resize', updatePosition)
  window.addEventListener('scroll', updatePosition, true)
  onCleanup(() => {
    document.removeEventListener('click', onOutsideClick, true)
    document.removeEventListener('keydown', onKeydown)
    window.removeEventListener('resize', updatePosition)
    window.removeEventListener('scroll', updatePosition, true)
  })
})

// Keep the selected snapshot if it ages out of the latest ten while being read.
watch(() => props.requests, requests => {
  if (!activeRequest.value) return
  const updated = requests.find(request => requestKey(request) === activeKey.value)
  if (updated) activeRequest.value = updated
})

onBeforeUnmount(cancelClose)
</script>
