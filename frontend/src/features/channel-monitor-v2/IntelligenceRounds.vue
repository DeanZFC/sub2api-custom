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
    <p v-if="loadError" class="text-xs text-gray-500">
      {{ tr('loadError') }} <button type="button" class="underline" @click="reload++">{{ t('common.retry') }}</button>
    </p>
    <p v-else-if="!rounds.length" class="text-xs text-gray-400">{{ busy ? t('common.loading') : tr('noRounds') }}</p>
    <template v-else>
      <div class="round-strip" :aria-label="tr('title')">
        <span v-for="n in Math.max(0, 20 - rounds.length)" :key="'blank' + n" class="round-filler" aria-hidden="true" />
        <button
          v-for="r in rounds"
          :key="r.id"
          type="button"
          class="round-cell"
          :aria-label="time(r.finished_at) + ' · ' + tr('legend.' + r.verdict)"
          :aria-expanded="active?.kind === 'round' && active.id === r.id"
          aria-haspopup="dialog"
          @focus="hover('round', r.id, $event)"
          @pointerenter="hover('round', r.id, $event)"
          @pointerleave="scheduleClose"
          @click="pin('round', r.id, $event)"
        >
          <i :class="colors[r.verdict]" aria-hidden="true" />
        </button>
      </div>
      <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[10px] text-gray-500 dark:text-gray-400">
        <span v-for="s in states" :key="s" class="inline-flex items-center gap-1"
          ><i class="h-2 w-2 rounded-sm" :class="colors[s]" />{{ tr('legend.' + s) }}</span
        >
      </div>
    </template>
    <div
      v-if="active"
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
          <p>
            {{
              header
                ? tr('accountRate', { pass: header.counts.pass, total: header.accounts })
                : tr('windowSize', { rounds: totals!.rounds, accounts: totals!.accounts, participations: totals!.participations })
            }}
            · {{ tr('modelRate', { pass: (header || totals)!.passed_targets, total: (header || totals)!.targets }) }}
          </p>
          <p class="flex flex-wrap gap-x-3 gap-y-1">
            <span v-for="s in states" :key="s"
              ><i class="mr-1 inline-block h-2 w-2 rounded-sm" :class="colors[s]" />{{ tr('states.' + s) }}
              {{ (header || totals)!.counts[s] }}</span
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
      v-if="art && detail"
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
  type IntelligenceCounts,
  type RoundAccount,
} from '@/api/intelligence'
import IntelligenceArtworkDialog from './IntelligenceArtworkDialog.vue'

const props = defineProps<{ group: IntelligenceGroup; model?: string; refreshKey?: number }>()
const { t, locale } = useI18n()
const tr = (key: string, params: Record<string, unknown> = {}) => t('channelMonitorV2.intelligence.' + key, params)
const states: IntelligenceVerdict[] = ['pass', 'suspect', 'degraded', 'unknown']
const colors: Record<IntelligenceVerdict, string> = {
  pass: 'bg-emerald-500',
  suspect: 'bg-amber-400',
  degraded: 'bg-red-500',
  unknown: 'bg-gray-400',
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
  [() => props.group.id, () => props.model, () => props.refreshKey, reload],
  async ([id, model], old, cleanup) => {
    const request = new AbortController()
    cleanup(() => request.abort())
    if (!old || old[0] !== id || old[1] !== model) {
      rounds.value = []
      dismiss(false)
    }
    busy.value = true
    loadError.value = false
    try {
      const rows = await intelligenceRounds(id, model, request.signal)
      if (request.signal.aborted) return
      rounds.value = rows
      if (active.value?.kind === 'round' && !rows.some((r) => r.id === active.value?.id)) dismiss(false)
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
  if (!scope) return
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
.round-strip {
  display: grid;
  grid-template-columns: repeat(20, minmax(0, 1fr));
  gap: 3px;
}
.round-cell,
.round-filler {
  min-width: 0;
  display: grid;
  align-items: center;
  height: 24px;
  border-radius: 3px;
}
.round-cell i,
.round-filler::before {
  content: '';
  display: block;
  height: 13px;
  border-radius: 3px;
}
.round-filler::before {
  background: rgb(148 163 184/0.14);
}
.round-cell[aria-expanded='true'] i,
.round-cell:focus-visible i {
  outline: 2px solid currentColor;
  outline-offset: 1px;
}
.round-body {
  max-height: min(48vh, 23rem);
}
@media (pointer: coarse) {
  .round-strip {
    grid-template-columns: repeat(10, minmax(0, 1fr));
  }
  .round-cell,
  .round-filler {
    height: 32px;
  }
}
</style>
