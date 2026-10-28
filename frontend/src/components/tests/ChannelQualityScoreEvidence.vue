<template>
  <div v-if="interactive" data-quality-score>
    <button
      ref="trigger"
      type="button"
      class="rounded px-1 font-mono text-lg font-semibold outline-none hover:bg-gray-50 focus-visible:ring-2 focus-visible:ring-primary-400 dark:hover:bg-dark-800"
      :class="qualityScoreClass(value)"
      :aria-label="`${t('admin.tests.quality.qualityScore')}: ${displayValue}`"
      :aria-expanded="open"
      aria-haspopup="dialog"
      @mouseenter="show"
      @mouseleave="scheduleClose"
      @focus="show"
      @blur="scheduleClose"
      @click.stop="togglePinned"
    >{{ displayValue }}</button>
    <Teleport to="body">
      <div
        v-if="open"
        ref="panel"
        role="dialog"
        :aria-label="tr('details')"
        class="fixed z-[99999] flex flex-col overflow-hidden rounded-lg border border-gray-200 bg-white text-left text-xs leading-relaxed text-gray-700 shadow-xl dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200"
        :style="panelStyle"
        @mouseenter="cancelClose"
        @mouseleave="scheduleClose"
        @focusin="cancelClose"
        @focusout="scheduleClose"
      >
        <div class="flex shrink-0 items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-700">
          <span class="font-semibold">{{ tr('details') }}</span>
          <button type="button" class="rounded p-1 text-gray-500 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700" :aria-label="t('common.close')" @click.stop="close">
            <Icon name="x" size="sm" />
          </button>
        </div>
        <div class="min-h-0 overflow-y-auto overscroll-contain p-3">
          <div class="mb-3 flex items-baseline gap-2 border-b border-gray-200 pb-3 dark:border-dark-700">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.tests.quality.qualityScore') }}</span>
            <strong class="font-mono text-xl" :class="qualityScoreClass(value)">{{ displayValue }}</strong>
            <span v-if="score?.status" class="text-amber-700 dark:text-amber-300">{{ tr('status.' + score.status) }}</span>
          </div>
          <ChannelQualityScoreDetails :score="score" />
        </div>
      </div>
    </Teleport>
  </div>
  <div v-else data-quality-score :title="t('admin.tests.quality.qualityScoreHint')">
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-xs text-gray-500">{{ t('admin.tests.quality.qualityScore') }}</span>
      <strong class="font-mono text-lg" :class="qualityScoreClass(value)">{{ displayValue }}</strong>
      <span v-if="!compact" class="text-xs text-gray-500">{{ score ? t('admin.tests.quality.' + (score.models > 0 ? 'qualityScoreSamples' : 'qualityScoreAccountSamples'), { samples: score.samples, models: score.models }) : tr('noScore') }}</span>
      <span v-if="score?.status && score.status !== 'ready'" class="text-xs text-amber-700 dark:text-amber-300" data-score-status>{{ tr('status.' + score.status) }}</span>
    </div>
    <details v-if="!compact && score?.dimensions?.length" class="mt-2 text-xs" data-score-evidence>
      <summary class="cursor-pointer text-gray-500">{{ tr('details') }} · {{ tr('coverage', { value: Math.round(score.coverage_percent) }) }}</summary>
      <ChannelQualityScoreDetails :score="score" class="mt-3" />
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, type CSSProperties } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ChannelQualityScore } from '@/api/admin/channelQuality'
import { qualityScoreClass, qualityScoreValue } from '@/utils/channelQualityScore'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import ChannelQualityScoreDetails from './ChannelQualityScoreDetails.vue'
import Icon from '@/components/icons/Icon.vue'
const props = defineProps<{ score?: ChannelQualityScore; compact?: boolean; interactive?: boolean }>()
const { t } = useI18n()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.scoring.${key}`, values)
const value = computed(() => qualityScoreValue(props.score))
const displayValue = computed(() => value.value == null ? '—' : String(Math.round(value.value)))
const trigger = ref<HTMLElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const panelStyle = ref<CSSProperties>({})
const open = ref(false)
let pinned = false
let closeTimer: ReturnType<typeof setTimeout> | undefined

function cancelClose() {
  clearTimeout(closeTimer)
  closeTimer = undefined
}
function scheduleClose() {
  if (pinned) return
  cancelClose()
  closeTimer = setTimeout(close, 180)
}
function updatePosition() {
  if (!trigger.value?.isConnected) return
  const position = getFloatingPanelPosition(trigger.value.getBoundingClientRect(), window.innerWidth, window.innerHeight, { maxWidth: 520, minComfortableHeight: 320 })
  panelStyle.value = {
    top: position.top == null ? undefined : `${position.top}px`,
    bottom: position.bottom == null ? undefined : `${position.bottom}px`,
    left: `${position.left}px`, width: `${position.width}px`, maxHeight: `${position.maxHeight}px`,
  }
}
function show() {
  cancelClose()
  if (open.value) return
  open.value = true
  updatePosition()
}
function togglePinned() {
  if (pinned) return close()
  show()
  pinned = true
}
function close() {
  cancelClose()
  open.value = false
  pinned = false
}
function outsideClick(event: MouseEvent) {
  if (event.target instanceof Node && !trigger.value?.contains(event.target) && !panel.value?.contains(event.target)) close()
}
function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape') close()
}
watch(open, (isOpen, _, onCleanup) => {
  if (!isOpen) return
  document.addEventListener('click', outsideClick, true)
  document.addEventListener('keydown', keydown)
  window.addEventListener('resize', updatePosition)
  window.addEventListener('scroll', updatePosition, true)
  onCleanup(() => {
    document.removeEventListener('click', outsideClick, true)
    document.removeEventListener('keydown', keydown)
    window.removeEventListener('resize', updatePosition)
    window.removeEventListener('scroll', updatePosition, true)
  })
})
watch(() => [props.score?.value, props.score?.status, props.score?.latest_at], close)
onBeforeUnmount(close)
</script>
