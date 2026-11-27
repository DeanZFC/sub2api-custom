<template>
  <section class="card space-y-5 p-5">
    <div>
      <h2 class="text-lg font-semibold">{{ t('channelMonitorV2.probes.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('channelMonitorV2.probes.hint') }}</p>
    </div>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="save">
      <label class="space-y-1 text-sm">
        <span>{{ t('channelMonitorV2.probes.group') }}</span>
        <select v-model="groupID" class="input" :disabled="loading || !!loadError" required @change="selectGroup">
          <option :value="null" disabled>{{ t('channelMonitorV2.probes.select') }}</option>
          <option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option>
        </select>
      </label>
      <label class="space-y-1 text-sm">
        <span>{{ t('channelMonitorV2.probes.model') }}</span>
        <input v-model.trim="model" class="input" list="group-probe-models" required maxlength="200" />
        <datalist id="group-probe-models">
          <option v-for="name in suggestions" :key="name" :value="name" />
        </datalist>
      </label>
      <label class="space-y-1 text-sm">
        <span>{{ t('channelMonitorV2.probes.windowMinutes') }}</span>
        <input v-model.number="rule.idle_window_minutes" name="idle_window_minutes" type="number" class="input w-28" min="1" max="1440" step="1" required />
      </label>
      <label class="space-y-1 text-sm">
        <span>{{ t('channelMonitorV2.probes.minRequests') }}</span>
        <input v-model.number="rule.min_requests" name="min_requests" type="number" class="input w-28" min="1" max="2147483647" step="1" required />
      </label>
      <label class="space-y-1 text-sm">
        <span>{{ t('channelMonitorV2.probes.duration') }}</span>
        <input v-model.number="rule.duration_minutes" name="duration_minutes" type="number" class="input w-28" min="1" max="1440" step="1" required />
      </label>
      <label class="space-y-1 text-sm">
        <span>{{ t('channelMonitorV2.probes.interval') }}</span>
        <input v-model.number="rule.interval_seconds" name="interval_seconds" type="number" class="input w-28" min="1" max="86400" step="1" required />
      </label>
      <button class="btn btn-primary" :disabled="busy || loading || !!loadError || !groupID || !model || !validRule || selectedPending">{{ t('channelMonitorV2.probes.save') }}</button>
    </form>

    <p v-if="errorText || loadError" role="alert" class="text-sm text-red-600">{{ errorText || loadError }}</p>
    <p v-if="!rows.length" class="text-sm text-gray-500">{{ loading ? t('common.loading') : t('channelMonitorV2.probes.empty') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="min-w-full text-left text-sm">
        <thead class="text-gray-500"><tr>
          <th v-for="column in columns" :key="column" scope="col" class="px-3 py-2">{{ t('channelMonitorV2.probes.' + column) }}</th>
        </tr></thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="p in rows" :key="p.group_id">
            <td class="px-3 py-3"><strong>{{ p.group_name }}</strong><div>{{ p.model }}</div>
              <div class="text-xs text-gray-500">{{ t('channelMonitorV2.probes.rule', { minutes: p.idle_window_minutes, count: p.min_requests, duration: p.duration_minutes, seconds: p.interval_seconds }) }}</div>
            </td>
            <td class="px-3 py-3 tabular-nums">{{ date(p.last_user_at) }}</td>
            <td class="px-3 py-3">
              <strong :class="color(p.status)">{{ t('channelMonitorV2.probes.states.' + p.status) }}</strong>
              <span v-if="p.pending" class="ml-2 text-gray-500">{{ t('channelMonitorV2.probes.pending') }}</span>
              <div class="tabular-nums">{{ date(p.last_probe_at) }}</div>
              <div v-if="p.round_until" class="text-xs">{{ t('channelMonitorV2.probes.roundUntil', { time: date(p.round_until) }) }}</div>
              <div v-if="p.deferred_reason || p.result?.detail">{{ detail(p.deferred_reason || p.result?.detail) }}</div>
              <div v-if="p.result?.switched">{{ t('channelMonitorV2.probes.switched') }}</div>
            </td>
            <td class="px-3 py-3 tabular-nums">{{ p.result?.first_token_ms != null ? p.result.first_token_ms + ' ms' : '—' }}</td>
            <td class="px-3 py-3 tabular-nums">
              {{ p.result?.estimated_cost_usd != null ? '$' + p.result.estimated_cost_usd.toFixed(6) : '—' }}
              <div v-if="p.result?.tokens != null">{{ p.result.tokens }} tokens</div>
              <div v-if="p.result?.partial_usage" class="text-xs text-gray-500">{{ t('channelMonitorV2.probes.partial') }}</div>
            </td>
            <td class="px-3 py-3"><div class="flex gap-2 whitespace-nowrap">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || p.pending || !p.enabled || p.status === 'disabled'" @click="act(() => runGroupProbe(p.group_id))">{{ t('channelMonitorV2.probes.run') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || (!p.enabled && p.pending)" @click="act(() => saveGroupProbe(p.group_id, p.model, !p.enabled))">{{ t(p.enabled ? 'channelMonitorV2.probes.pause' : 'channelMonitorV2.probes.resume') }}</button>
            </div></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAll, getModelAllowlistCandidates } from '@/api/admin/groups'
import { listGroupProbes, runGroupProbe, saveGroupProbe, type GroupProbe, type GroupProbeRule } from '@/api/admin/groupProbes'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'

const { t, locale } = useI18n()
const columns = ['target', 'lastUser', 'result', 'ttft', 'cost', 'actions'] as const
const groups = ref<AdminGroup[]>([])
const rows = ref<GroupProbe[]>([])
const groupID = ref<number | null>(null)
const model = ref('')
const defaults: GroupProbeRule = { idle_window_minutes: 5, min_requests: 5, duration_minutes: 10, interval_seconds: 30 }
const rule = reactive({ ...defaults })
const positiveInt = (value: number, max: number) => Number.isInteger(value) && value >= 1 && value <= max
const validRule = computed(() => positiveInt(rule.idle_window_minutes, 1440) && positiveInt(rule.min_requests, 2147483647)
  && positiveInt(rule.duration_minutes, 1440) && positiveInt(rule.interval_seconds, 86400))
const busy = ref(false)
const loading = ref(true)
const errorText = ref('')
const loadError = ref('')
const catalog = ref<string[]>([])
const suggestions = computed(() => {
  const allowed = groups.value.find(g => g.id === groupID.value)?.model_allowlist?.models ?? []
  return [...new Set([...allowed, ...catalog.value])].filter(name => !name.includes('*'))
})
const selectedPending = computed(() => rows.value.find(p => p.group_id === groupID.value)?.pending)
const stop = new AbortController()
let timer: ReturnType<typeof setInterval>
let generation = 0

async function selectGroup() {
  const group = groups.value.find(g => g.id === groupID.value)
  catalog.value = []
  const saved = rows.value.find(p => p.group_id === groupID.value)
  model.value = saved?.model ?? ''
  rule.idle_window_minutes = saved?.idle_window_minutes ?? defaults.idle_window_minutes
  rule.min_requests = saved?.min_requests ?? defaults.min_requests
  rule.duration_minutes = saved?.duration_minutes ?? defaults.duration_minutes
  rule.interval_seconds = saved?.interval_seconds ?? defaults.interval_seconds
  if (!group) return
  try {
    const models = await getModelAllowlistCandidates(group.id, group.platform)
    if (!stop.signal.aborted && group.id === groupID.value) catalog.value = models
  } catch (err) {
    if (!stop.signal.aborted && group.id === groupID.value) errorText.value = extractApiErrorMessage(err, t('common.error'))
  }
}
function date(value: string | null) {
  return value ? new Date(value).toLocaleString(locale.value) : '—'
}
function color(status: GroupProbe['status']) {
  if (status === 'healthy' || status === 'recovered') return 'text-green-600'
  if (status === 'failed') return 'text-red-600'
  if (status === 'confirming' || status === 'stale') return 'text-amber-600'
  return 'text-gray-500'
}
function detail(reason?: string) {
  const keys = ['timeout','empty_response','incomplete_stream','budget_unavailable','group_unavailable','budget_exhausted']
  return reason && keys.includes(reason) ? t('channelMonitorV2.probes.reasons.' + reason) : reason
}
async function refresh() {
  const seq = ++generation
  try {
    const result = await listGroupProbes(stop.signal)
    if (seq === generation && !stop.signal.aborted) {
      rows.value = result
      loadError.value = ''
    }
  } catch (err) {
    if (seq === generation && !stop.signal.aborted) loadError.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}
async function act(mutate: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  errorText.value = ''
  try {
    await mutate()
    await refresh()
  } catch (err) {
    errorText.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    busy.value = false
  }
}
function save() {
  const id = groupID.value
  if (id && model.value && validRule.value && !selectedPending.value && !loading.value && !loadError.value) {
    void act(() => saveGroupProbe(id, model.value, true, { ...rule }))
  }
}
onMounted(async () => {
  timer = setInterval(() => { if (!document.hidden && !busy.value) void refresh() }, 5000)
  void refresh()
  try {
    const items = await getAll()
    groups.value = items.filter(g => g.platform !== 'typesafe' && (!g.claude_code_only || g.fallback_group_id != null))
  } catch (err) {
    errorText.value = extractApiErrorMessage(err, t('common.error'))
  }
})
onUnmounted(() => { clearInterval(timer); stop.abort() })
</script>
