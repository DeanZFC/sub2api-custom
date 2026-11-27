<template>
  <section
    ref="root"
    class="relative my-3 min-w-0"
    data-testid="intelligence-rounds"
    :aria-label="tr('title')"
    @keydown.esc="escapePanel"
    @focusout="leaveFocus"
  >
    <div class="mb-1 flex items-center justify-between gap-2 text-xs">
      <h4 class="font-medium text-gray-700 dark:text-gray-200">{{ tr('title') }}</h4>
      <button
        v-if="rounds.length"
        type="button"
        class="text-emerald-700 dark:text-emerald-400"
        :aria-expanded="active?.kind === 'summary'"
        aria-haspopup="dialog"
        @focus="hover('summary', '', $event)"
        @pointerenter="hover('summary', '', $event)"
        @pointerleave="scheduleClose"
        @click="pin('summary', '', $event)"
      >
        {{ tr('summary') }} ›
      </button>
    </div>
    <div class="monitor-status-strip monitor-status-strip-compact" :aria-label="tr('title')">
      <span
        v-for="n in MONITOR_HISTORY_SLOTS - rounds.length"
        :key="'blank' + n"
        class="monitor-status-slot"
        data-state="unknown"
        aria-hidden="true"
      >
        <span class="monitor-status-mark" :class="colors.unknown" />
      </span>
      <button
        v-for="r in roundCells"
        :key="r.id"
        type="button"
        class="monitor-status-slot round-cell"
        :data-state="r.state"
        :aria-label="time(r.finished_at) + ' · ' + tr('legend.' + r.state) + ' · ' + roundAccountLabel(r)"
        :aria-expanded="active?.kind === 'round' && active.id === r.id"
        aria-haspopup="dialog"
        @focus="hover('round', r.id, $event)"
        @pointerenter="hover('round', r.id, $event)"
        @pointerleave="scheduleClose"
        @click="pin('round', r.id, $event)"
      >
        <span class="monitor-status-mark" :class="colors[r.state]" aria-hidden="true" />
      </button>
    </div>
    <p v-if="loadError" class="mt-1 text-xs text-gray-500">
      {{ tr('loadError') }} <button type="button" class="underline" @click="reload++">{{ t('common.retry') }}</button>
    </p>
    <p v-else-if="!rounds.length" class="mt-1 text-xs text-gray-400">{{ emptyMessage }}</p>
    <div v-else class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[10px] text-gray-500 dark:text-gray-400">
      <span v-for="(color, s) in colors" :key="s" class="inline-flex items-center gap-1">
        <i class="h-2 w-2 rounded-sm" :class="color" />{{ tr('legend.' + s) }}
      </span>
      <span>{{ tr('modelThreshold') }}</span>
    </div>
    <div
      v-if="active && group"
      role="dialog"
      :aria-label="active.kind === 'round' ? tr('round') : tr('summary')"
      :class="panelBelow ? 'top-full mt-1' : 'bottom-full mb-1'"
      :style="{ maxHeight: panelRoom + 'px' }"
      class="round-panel absolute inset-x-0 z-30 flex flex-col overflow-hidden rounded-xl border border-gray-200 bg-white text-xs shadow-xl dark:border-dark-700 dark:bg-dark-800"
      @pointerenter="clearClose"
      @pointerleave="scheduleClose"
      @focusin="clearClose"
    >
      <header class="flex shrink-0 items-center justify-between gap-2 border-b border-gray-100 px-3 py-2 dark:border-dark-700">
        <div>
          <strong>{{ group.name }} · {{ active.kind === 'round' ? tr('round') : tr('summary') }}</strong>
          <p v-if="header" class="mt-0.5 text-gray-500">{{ time(header.started_at) }} – {{ time(header.finished_at) }}</p>
          <p class="text-gray-500">{{ model || t('channelMonitorV2.filters.allModels') }}</p>
        </div>
        <button ref="closeButton" type="button" class="p-2" :aria-label="t('common.close')" @click="dismiss(true)">×</button>
      </header>
      <div ref="scroller" class="round-body min-h-0 space-y-2 overflow-y-auto overscroll-contain px-3 py-2">
        <template v-if="header || totals">
          <ul v-if="header" class="space-y-0.5">
            <li v-for="m in header.model_counts" :key="m.model">
              {{ m.model }} · {{ m.pass + m.degraded ? tr('modelNormalRate', { pass: m.pass, total: m.pass + m.degraded }) : tr('noDecidedAccounts') }}
            </li>
          </ul>
          <p>
            {{
              header
                ? roundAccountLabel(header)
                : tr('windowSize', { rounds: totals!.rounds, accounts: totals!.accounts, participations: totals!.participations })
            }}
            · {{ tr('modelRate', { pass: (header || totals)!.passed_targets, total: (header || totals)!.targets }) }}
          </p>
          <p v-if="totals && !header" class="flex flex-wrap gap-x-3 gap-y-1">
            <span v-for="s in states" :key="s"
              ><i class="mr-1 inline-block h-2 w-2 rounded-sm" :class="colors[s === 'suspect' ? 'unknown' : s]" />{{ tr('states.' + s) }}
              {{ totals.counts[s] }}</span
            >
          </p>
        </template>
        <p v-if="detailBusy" role="status">{{ t('common.loading') }}</p>
        <p v-else-if="detailError" role="alert">
          {{ tr('loadError') }} <button type="button" class="underline" @click="detailRetry++">{{ t('common.retry') }}</button>
        </p>
        <template v-else-if="detail">
          <details
            v-for="a in detail.accounts"
            :key="a.alias"
            class="border-t border-gray-100 py-1.5 dark:border-dark-700"
            @toggle="keepExpanded"
          >
            <summary class="cursor-pointer">
              {{ tr('account') }} {{ a.alias }} · {{ tr('states.' + a.verdict) }} · {{ a.passed_targets }}/{{ a.targets }}
            </summary>
            <section
              v-for="m in a.models"
              :key="JSON.stringify([m.model, m.effort])"
              class="mt-1 rounded-md bg-gray-50 p-2 dark:bg-dark-900"
            >
              <h5 class="break-words font-semibold">
                {{ m.model }} · {{ m.effort || t('channelMonitorV2.intelligence.defaultEffort') }} ·
                {{ tr('states.' + m.verdict) }}
              </h5>
              <p class="text-gray-500">{{ time(m.finished_at) }}</p>
              <dl class="mt-1 space-y-1">
                <div v-for="c in m.checks" :key="c.key" class="flex items-baseline justify-between gap-2">
                  <dt>{{ tr('checks.' + c.key) }}</dt>
                  <dd class="break-words text-right">
                    {{ tr('checkStates.' + c.status) }}<span v-if="c.detail"> · {{ c.detail }}</span>
                  </dd>
                </div>
              </dl>
              <div class="mt-1 flex flex-wrap items-center justify-between gap-1">
                <span>{{ tr('artwork') }} · {{ tr('art.' + m.artwork_status) }}</span
                ><button
                  v-if="m.artwork_id"
                  type="button"
                  class="text-emerald-700 underline dark:text-emerald-400"
                  aria-haspopup="dialog"
                  @click="openArtwork(a, m, $event)"
                >
                  {{ tr('viewArtwork') }}
                </button>
              </div>
            </section>
          </details>
        </template>
        <template v-else-if="totals">
          <details
            v-for="a in totals.items"
            :key="a.alias"
            class="border-t border-gray-100 py-1.5 dark:border-dark-700"
            @toggle="keepExpanded"
          >
            <summary class="cursor-pointer">
              {{ tr('account') }} {{ a.alias }} · {{ tr('passRounds', { pass: a.counts.pass, total: a.participations }) }}
            </summary>
            <p class="my-1">{{ countLabel(a.counts) }}</p>
            <div
              v-for="m in a.models"
              :key="JSON.stringify([m.model, m.effort])"
              class="mb-1 rounded-md bg-gray-50 p-1.5 dark:bg-dark-900"
            >
              <strong>{{ m.model }} · {{ m.effort || t('channelMonitorV2.intelligence.defaultEffort') }}</strong>
              <p>{{ countLabel(m.counts) }}</p>
              <span>{{ tr('completed') }} · {{ time(m.last_tested_at) }}</span>
            </div>
          </details>
        </template>
        <div v-if="pages > 1" class="flex items-center justify-between gap-2">
          <button type="button" :disabled="page <= 1" @click="changePage(-1)">‹ {{ tr('prev') }}</button
          ><span>{{ page }}/{{ pages }}</span
          ><button type="button" :disabled="page >= pages" @click="changePage(1)">{{ tr('next') }} ›</button>
        </div>
      </div>
    </div>
    <IntelligenceArtworkDialog
      v-if="art && detail && group"
      :group="group"
      :round="detail.round"
      :accounts="detail.accounts"
      :initial-alias="art.alias"
      :initial-model="art.model"
      @close="closeArtwork"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  intelligenceRounds,
  intelligenceRound,
  intelligenceSummary,
  type IntelligenceGroup,
  type IntelligenceVerdict,
  type IntelligenceRound,
  type RoundDetail,
  type IntelligenceSummary,
  type IntelligenceModel,
  type IntelligenceModelCounts,
  type IntelligenceCounts,
  type RoundAccount,
} from '@/api/intelligence'
import IntelligenceArtworkDialog from './IntelligenceArtworkDialog.vue'
import { MONITOR_HISTORY_SLOTS } from './monitorCards'
import './monitorHealthColors.css'
import './monitorStatusStrip.css'

const props = defineProps<{ group?: IntelligenceGroup; directoryMessage?: string; model?: string; refreshKey?: number }>()
const { t, locale } = useI18n()
const tr = (key: string, params: Record<string, unknown> = {}) => t('channelMonitorV2.intelligence.' + key, params)
const states: IntelligenceVerdict[] = ['pass', 'suspect', 'degraded', 'unknown']
const colors = {
  pass: 'health-score10',
  partial: 'health-score6',
  degraded: 'health-critical',
  unknown: 'health-unknown',
}
function roundState(models: IntelligenceModelCounts[]): keyof typeof colors {
  const decided = models.filter(m => m.pass + m.degraded > 0)
  const passing = decided.filter(m => m.pass > 0).length
  if (passing > 0 && passing < decided.length) return 'partial'
  if (decided.length === 0 || decided.length < models.length) return 'unknown'
  return passing > 0 ? 'pass' : 'degraded'
}
function roundAccountLabel(round: IntelligenceRound) {
  return states
    .filter((state) => round.counts[state] > 0)
    .map((state) => tr('accountRate', { count: round.counts[state], total: round.accounts, state: tr('states.' + state) }))
    .join(' · ')
}
const countLabel = (counts: IntelligenceCounts) => states.map((s) => tr('states.' + s) + ' ' + counts[s]).join(' · ')
const time = (stamp: string | null) => (stamp ? new Date(stamp).toLocaleString(locale.value) : '—')
const root = ref<HTMLElement>(),
  closeButton = ref<HTMLButtonElement>(),
  scroller = ref<HTMLElement>()
const panelBelow = ref(false),
  panelRoom = ref(420)
const rounds = ref<IntelligenceRound[]>([]),
  busy = ref(false),
  loadError = ref(false),
  reload = ref(0)
const roundCells = computed(() => rounds.value.map((round) => ({
  ...round,
  state: roundState(round.model_counts ?? []),
})))
const emptyMessage = computed(() => {
  if (!props.group && props.directoryMessage) return props.directoryMessage
  return busy.value ? t('common.loading') : tr('noRounds')
})
const active = ref<{ kind: 'summary' | 'round'; id: string } | null>(null),
  page = ref(1),
  detailRetry = ref(0)
const detail = ref<RoundDetail>(),
  totals = ref<IntelligenceSummary>(),
  detailBusy = ref(false),
  detailError = ref(false)
const pages = computed(() => {
  const data = detail.value || totals.value
  return data && data.page_size > 0 ? Math.ceil(data.total / data.page_size) : 0
})
const header = computed(() => (active.value?.kind === 'round' ? rounds.value.find((r) => r.id === active.value?.id) : undefined))
const art = ref<{ alias: string; model: IntelligenceModel } | null>(null)
let pinned = false,
  restoring = false,
  anchor: HTMLElement | undefined,
  artButton: HTMLElement | undefined,
  artScroll = 0
let timer: ReturnType<typeof setTimeout> | undefined
function clearClose() {
  clearTimeout(timer)
}
function scheduleClose() {
  clearClose()
  if (!pinned && !art.value) timer = setTimeout(() => dismiss(false), 220)
}
function setActive(kind: 'round' | 'summary', id: string, e: Event) {
  clearClose()
  anchor = e.currentTarget as HTMLElement
  const box = root.value?.getBoundingClientRect()
  if (box) {
    const above = box.top - 12,
      below = window.innerHeight - box.bottom - 12
    panelBelow.value = above < 400 && below > above
    panelRoom.value = Math.max(100, panelBelow.value ? below : above)
  }
  if (active.value?.kind === kind && active.value.id === id) return
  active.value = { kind, id }
  page.value = 1
}
function hover(kind: 'round' | 'summary', id: string, e: Event) {
  if (restoring || pinned || ('pointerType' in e && e.pointerType !== 'mouse')) return
  setActive(kind, id, e)
}
function pin(kind: 'round' | 'summary', id: string, e: MouseEvent) {
  if (pinned && active.value?.kind === kind && active.value.id === id) {
    dismiss(false)
    return
  }
  pinned = true
  setActive(kind, id, e)
  if (e.detail === 0) nextTick(() => closeButton.value?.focus())
}
function dismiss(restore: boolean) {
  clearClose()
  pinned = false
  active.value = null
  art.value = null
  if (restore) {
    restoring = true
    anchor?.focus({ preventScroll: true })
    restoring = false
  }
}
function leaveFocus(e: FocusEvent) {
  if (!art.value && e.relatedTarget instanceof Node && !root.value?.contains(e.relatedTarget)) dismiss(false)
}
function escapePanel(e: KeyboardEvent) {
  if (!art.value && active.value) {
    e.preventDefault()
    dismiss(true)
  }
}
function outside(e: PointerEvent) {
  if (!art.value && !root.value?.contains(e.target as Node)) dismiss(false)
}
function keepExpanded(e: Event) {
  if ((e.target as HTMLDetailsElement).open) {
    pinned = true
    clearClose()
  }
}
function changePage(delta: number) {
  pinned = true
  page.value += delta
}
function openArtwork(a: RoundAccount, m: IntelligenceModel, e: Event) {
  pinned = true
  clearClose()
  artButton = e.currentTarget as HTMLElement
  artScroll = scroller.value?.scrollTop || 0
  art.value = { alias: a.alias, model: m }
}
function closeArtwork() {
  art.value = null
  nextTick(() => {
    artButton?.focus({ preventScroll: true })
    if (scroller.value) scroller.value.scrollTop = artScroll
  })
}
watch(
  [() => props.group?.id, () => props.model, () => props.refreshKey, reload],
  async ([id, model], old, cleanup) => {
    if (!old || old[0] !== id || old[1] !== model) {
      rounds.value = []
      dismiss(false)
    }
    loadError.value = false
    busy.value = id !== undefined
    if (id === undefined) return
    const request = new AbortController()
    cleanup(() => request.abort())
    try {
      const rows = await intelligenceRounds(id, model, request.signal)
      if (request.signal.aborted) return
      rounds.value = rows.slice(-MONITOR_HISTORY_SLOTS)
      if (active.value?.kind === 'round' && !rounds.value.some((r) => r.id === active.value?.id)) dismiss(false)
      else if (active.value && !art.value) detailRetry.value++
    } catch {
      if (!request.signal.aborted) {
        rounds.value = []
        dismiss(false)
        loadError.value = true
      }
    } finally {
      if (!request.signal.aborted) busy.value = false
    }
  },
  { immediate: true },
)
watch([active, page, detailRetry], async ([scope, pageNumber], old, cleanup) => {
  const request = new AbortController()
  cleanup(() => request.abort())
  const changed = old[0] !== scope || old[1] !== pageNumber
  if (changed) {
    detail.value = undefined
    totals.value = undefined
  }
  detailError.value = false
  if (!scope || !props.group) return
  detailBusy.value = !detail.value && !totals.value
  try {
    if (scope.kind === 'summary') {
      const data = await intelligenceSummary(props.group.id, props.model, pageNumber, request.signal)
      if (!request.signal.aborted) totals.value = data
    } else {
      const data = await intelligenceRound(props.group.id, scope.id, props.model, pageNumber, request.signal)
      if (!request.signal.aborted) detail.value = data
    }
    if (changed && !request.signal.aborted && scroller.value) scroller.value.scrollTop = 0
  } catch {
    if (!request.signal.aborted) detailError.value = true
  } finally {
    if (!request.signal.aborted) detailBusy.value = false
  }
})
onMounted(() => document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => {
  clearClose()
  document.removeEventListener('pointerdown', outside)
})
</script>
<style scoped>
.round-cell[aria-expanded='true'] .monitor-status-mark {
  outline: 2px solid currentColor;
  outline-offset: 1px;
}
.round-body {
  max-height: min(48vh, 23rem);
}
</style>
