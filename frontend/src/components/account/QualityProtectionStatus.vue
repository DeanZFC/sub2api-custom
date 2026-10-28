<template>
  <button
    ref="trigger"
    type="button"
    class="flex cursor-help flex-col items-center gap-1 rounded outline-none focus-visible:ring-2 focus-visible:ring-primary-400"
    data-testid="quality-paused-status"
    aria-haspopup="dialog"
    :aria-expanded="open"
    :aria-label="t('admin.accounts.qualityDetails.title')"
    @mouseenter="show"
    @mouseleave="scheduleClose"
    @focus="show"
    @blur="scheduleClose"
    @click.stop="togglePinned"
  >
    <span class="badge text-xs badge-warning">{{ t('admin.accounts.status.qualityPaused') }}</span>
    <span v-if="!account.schedulable" class="text-[11px] text-gray-500 dark:text-gray-400">
      {{ t('admin.accounts.status.qualityPausedManualStop') }}
    </span>
    <span v-if="reason" class="line-clamp-2 max-w-[180px] break-words text-center text-[11px] leading-4 text-gray-500 dark:text-gray-400">{{ reason }}</span>
  </button>
  <Teleport to="body">
    <div
      v-if="open"
      ref="panel"
      role="dialog"
      :aria-label="t('admin.accounts.qualityDetails.title')"
      class="fixed z-[99999] flex flex-col overflow-hidden rounded-xl bg-gray-900 text-left text-xs leading-relaxed text-gray-200 shadow-xl ring-1 ring-white/10 dark:bg-gray-800"
      :style="panelStyle"
      @mouseenter="cancelClose"
      @mouseleave="scheduleClose"
      @focusin="cancelClose"
      @focusout="scheduleClose"
    >
      <div class="flex shrink-0 items-center justify-between border-b border-white/10 px-4 py-3">
        <span class="text-sm font-semibold text-amber-300">{{ t('admin.accounts.qualityDetails.title') }}</span>
        <button type="button" class="rounded p-1 text-gray-400 hover:bg-white/10 hover:text-white" :aria-label="t('common.close')" @click.stop="close">
          <Icon name="x" size="sm" />
        </button>
      </div>
      <div class="min-h-0 space-y-3 overflow-y-auto overscroll-contain break-words p-4">
        <p v-if="reason" class="text-amber-200">{{ reason }}</p>
        <p v-if="loading" role="status">{{ t('common.loading') }}</p>
        <div v-else-if="loadError" role="alert">
          {{ t('admin.accounts.qualityDetails.loadFailed') }}
          <button type="button" class="ml-2 text-primary-300 underline" @click.stop="loadDetails">{{ t('common.retry') }}</button>
        </div>
        <p v-else-if="!holds.length" class="text-gray-400">{{ t('admin.accounts.qualityDetails.empty') }}</p>
        <template v-else>
          <p class="text-[11px] text-gray-400">{{ t('admin.accounts.qualityDetails.evidenceHint') }}</p>
          <section v-for="(hold, index) in holds" :key="`${hold.plan_id}-${index}`" class="space-y-3 rounded-lg border border-white/10 p-3">
            <div>
              <div class="flex flex-wrap items-baseline gap-x-2 font-semibold text-white">{{ hold.plan_name || `#${hold.plan_id}` }}<span v-if="hold.model_id" class="break-all font-mono text-[11px] font-normal text-gray-300" data-hold-model>{{ hold.model_id }}</span></div>
              <p v-if="hold.reason !== reason" class="mt-1 text-amber-200">{{ hold.reason }}</p>
              <div class="mt-1 text-[11px] text-gray-400">{{ t('admin.accounts.qualityDetails.updatedAt', { time: formatDateTime(hold.updated_at) }) }}</div>
              <p v-if="hold.prior_round" class="mt-1 text-amber-300">{{ t('admin.accounts.qualityDetails.retained') }}</p>
            </div>
            <section v-if="hold.trigger_snapshot" class="space-y-2 rounded border border-amber-400/30 p-2" data-trigger-snapshot>
              <h4 class="font-semibold text-amber-200">{{ t('admin.tests.experience.triggerSnapshot') }}</h4>
              <p>{{ formatDateTime(hold.trigger_snapshot.occurred_at) }} · {{ hold.trigger_snapshot.model_id }}</p>
              <p>{{ hold.trigger_snapshot.reason }}</p>
              <p class="break-all text-[11px] text-gray-400">{{ t('admin.tests.experience.triggerVersion', { version: hold.trigger_snapshot.policy_version, run: hold.trigger_snapshot.run_id || '—' }) }}</p>
              <div v-for="rule in hold.trigger_snapshot.rules" :key="rule.id"><strong>{{ rule.name }}</strong><p v-if="rule.condition">{{ conditionText(rule.condition, hold.trigger_snapshot) }}</p></div>
              <div v-for="test in hold.trigger_snapshot.tests" :key="test.test_definition_id" class="space-y-1 border-t border-white/10 pt-2">
                <strong>{{ test.test_name || `#${test.test_definition_id}` }} · {{ verdictText(test.verdict) }}</strong>
                <p class="text-[11px] text-gray-400">#{{ test.result_id || '—' }} · {{ formatDateTime(test.started_at) }}</p>
                <p v-if="test.window_start || test.window_end" class="text-[11px] text-gray-400">{{ formatDateTime(test.window_start) }} — {{ formatDateTime(test.window_end) }}</p>
                <p>{{ test.reason }}</p>
                <div v-for="(threshold, ti) in test.thresholds" :key="ti" class="text-[11px]">
                  {{ t(`admin.accounts.qualityDetails.metrics.${threshold.metric}`) }} {{ threshold.operator === 'lt' ? '&lt;' : '&gt;' }} {{ metricValue(threshold.value, threshold.metric) }} · {{ t('admin.accounts.qualityDetails.observed') }}：{{ threshold.observed == null ? '—' : metricValue(threshold.observed, threshold.metric) }}
                  <p>{{ t('admin.tests.experience.triggerSamples', { samples: threshold.samples ?? '—', minimum: test.min_samples ?? '—' }) }}</p>
                </div>
              </div>
            </section>
            <p v-else class="text-amber-300" data-trigger-missing>{{ t('admin.tests.experience.triggerMissing') }}</p>
            <h4 class="font-semibold text-gray-200">{{ t('admin.tests.experience.currentObservation') }}</h4>
            <div v-for="rule in hold.rules" :key="rule.id" class="space-y-1 border-l-2 border-amber-400/50 pl-2">
              <div class="font-medium text-amber-200">{{ t('admin.accounts.qualityDetails.hitRule', { name: rule.name }) }}</div>
              <p v-if="rule.condition">{{ conditionText(rule.condition, hold) }}</p>
              <p v-else class="text-gray-400">{{ t('admin.accounts.qualityDetails.conditionUnavailable') }}</p>
              <p class="text-[11px] text-gray-400">{{ t('admin.accounts.qualityDetails.currentMatch') }}：{{ matchText(rule.current_match) }}</p>
            </div>
            <div v-for="test in hold.tests" :key="test.test_definition_id" class="space-y-1 border-t border-white/10 pt-2">
              <div class="flex items-start justify-between gap-3">
                <span class="font-medium text-white">{{ test.test_name || `#${test.test_definition_id}` }}</span>
                <span class="shrink-0" :class="test.verdict === 'fail' ? 'text-red-300' : test.verdict === 'pass' ? 'text-emerald-300' : 'text-gray-400'">{{ verdictText(test.verdict) }}</span>
              </div>
              <p v-if="test.result_id" class="text-[11px] text-gray-400">#{{ test.result_id }} · {{ formatDateTime(test.started_at) }}</p>
              <p v-if="!test.current" class="text-amber-300">{{ t('admin.accounts.qualityDetails.waiting') }}</p>
              <template v-else>
                <p>{{ t('admin.accounts.qualityDetails.automatic') }}：{{ verdictText(test.automated_verdict) }}<template v-if="test.admin_verdict"> · {{ t('admin.accounts.qualityDetails.admin') }}：{{ verdictText(test.admin_verdict) }}</template></p>
                <p v-if="test.reason">{{ test.reason }}</p>
                <p v-if="test.automatic_reason && test.automatic_reason !== test.reason" class="text-gray-400">{{ test.automatic_reason }}</p>
                <p v-if="test.vote?.enabled && test.vote.public_enabled" class="text-gray-400">{{ t('admin.accounts.qualityDetails.votes', { pass: test.pass_count, fail: test.fail_count, reject: test.vote.reject_above, required: test.vote.pass_at_least }) }}</p>
              </template>
              <div v-for="(threshold, thresholdIndex) in test.thresholds" :key="thresholdIndex" class="flex flex-wrap justify-between gap-x-3 text-[11px] text-gray-300">
                <span>{{ t(`admin.accounts.qualityDetails.metrics.${threshold.metric}`) }} {{ threshold.operator === 'lt' ? '&lt;' : '&gt;' }} {{ metricValue(threshold.value, threshold.metric) }}</span>
                <span>{{ t('admin.accounts.qualityDetails.observed') }}：{{ threshold.observed == null ? '—' : metricValue(threshold.observed, threshold.metric) }}</span>
              </div>
            </div>
          </section>
        </template>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, type CSSProperties } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getAccountProtection, type QualityProtectionHold } from '@/api/admin/tests'
import type { Account, TestCombinationCondition } from '@/types'
import { formatDateTime } from '@/utils/format'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'

const props = defineProps<{ account: Account }>()
const { t } = useI18n()
const reason = computed(() => typeof props.account.extra?.quality_protection_reason === 'string' ? props.account.extra.quality_protection_reason.trim() : '')
const trigger = ref<HTMLElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const panelStyle = ref<CSSProperties>({})
const open = ref(false)
const loading = ref(false)
const loadError = ref(false)
const holds = ref<QualityProtectionHold[]>([])
let pinned = false
let requestVersion = 0
let closeTimer: ReturnType<typeof setTimeout> | undefined

const verdictText = (verdict: string) => t(`admin.accounts.qualityDetails.${verdict === 'pass' || verdict === 'fail' ? verdict : 'pending'}`)
const matchText = (verdict: string) => t(`admin.accounts.qualityDetails.${verdict === 'pass' ? 'matched' : verdict === 'fail' ? 'unmatched' : 'pending'}`)
function conditionText(condition: TestCombinationCondition, hold: Pick<QualityProtectionHold, 'tests'>): string {
  if (condition.operator === 'test') {
    const name = hold.tests.find(test => test.test_definition_id === condition.test_definition_id)?.test_name || `#${condition.test_definition_id}`
    return `${name} = ${verdictText(condition.verdict || 'pending')}`
  }
  return `${t(`admin.accounts.qualityDetails.${condition.operator === 'all' ? 'all' : 'any'}`)}（${(condition.conditions || []).map(child => conditionText(child, hold)).join('；')}）`
}
function metricValue(value: number, metric: string) {
  const unit = metric.endsWith('_rate') ? '%' : metric.endsWith('_ms') ? ' ms' : ''
  return `${Number(value.toPrecision(6))}${unit}`
}

function cancelClose() { clearTimeout(closeTimer) }
function scheduleClose() {
  if (pinned) return
  cancelClose()
  closeTimer = setTimeout(close, 180)
}
function updatePosition() {
  if (!trigger.value?.isConnected) return
  const position = getFloatingPanelPosition(trigger.value.getBoundingClientRect(), window.innerWidth, window.innerHeight, { maxWidth: 440, minComfortableHeight: 380 })
  panelStyle.value = {
    top: position.top == null ? undefined : `${position.top}px`,
    bottom: position.bottom == null ? undefined : `${position.bottom}px`,
    left: `${position.left}px`, width: `${position.width}px`, maxHeight: `${position.maxHeight}px`,
  }
}
async function loadDetails() {
  const version = ++requestVersion
  loading.value = true
  loadError.value = false
  holds.value = []
  try {
    const data = await getAccountProtection(props.account.id)
    if (version === requestVersion) holds.value = data
  } catch {
    if (version === requestVersion) loadError.value = true
  } finally {
    if (version === requestVersion) loading.value = false
  }
}
function show() {
  cancelClose()
  if (open.value) return
  open.value = true
  updatePosition()
  void loadDetails()
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
  requestVersion++
}
function outsideClick(event: MouseEvent) {
  if (event.target instanceof Node && !trigger.value?.contains(event.target) && !panel.value?.contains(event.target)) close()
}
function keydown(event: KeyboardEvent) { if (event.key === 'Escape') close() }
watch(open, (value, _, onCleanup) => {
  if (!value) return
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
watch(() => [props.account.id, props.account.status, reason.value, props.account.updated_at], close)
onBeforeUnmount(close)
</script>
