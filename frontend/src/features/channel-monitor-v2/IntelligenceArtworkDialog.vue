<template>
  <dialog
    ref="dialog"
    class="m-auto max-h-[88dvh] w-[min(40rem,94vw)] overflow-auto rounded-xl border border-gray-200 bg-white p-4 text-gray-800 shadow-xl backdrop:bg-black/40 dark:border-dark-700 dark:bg-dark-800 dark:text-white"
    :aria-label="t('channelMonitorV2.intelligence.artwork')"
    @close="emit('close')"
    @click="onBackdrop"
  >
    <header class="mb-3 flex items-center justify-between gap-3">
      <div>
        <p class="text-xs text-gray-500">{{ group.name }} · {{ time(round.finished_at) }}</p>
        <h3 class="font-semibold">{{ t('channelMonitorV2.intelligence.artwork') }}</h3>
      </div>
      <button class="btn btn-secondary btn-sm" type="button" autofocus @click="dialog?.close()">{{ t('common.close') }}</button>
    </header>
    <div class="mb-3 grid grid-cols-2 gap-2 text-sm">
      <label
        >{{ t('channelMonitorV2.intelligence.account') }}
        <select v-model="alias" class="input w-full" @change="targetKey = ''">
          <option v-for="a in accounts" :key="a.alias" :value="a.alias">{{ a.alias }}</option>
        </select>
      </label>
      <label
        >{{ t('tests.modelFilter') }}
        <select
          :value="target && key(target)"
          class="input w-full"
          @change="targetKey = ($event.target as HTMLSelectElement).value"
        >
          <option v-for="m in account?.models" :key="key(m)" :value="key(m)">
            {{ m.model }} · {{ m.effort || t('channelMonitorV2.intelligence.defaultEffort') }}
          </option>
        </select>
      </label>
    </div>
    <template v-if="target">
      <p v-if="loading" role="status" class="p-4 text-sm">{{ t('common.loading') }}</p>
      <p v-else-if="error" class="p-4 text-sm" role="alert">
        {{ t('channelMonitorV2.intelligence.artError') }}
        <button type="button" class="underline" @click="retry++">{{ t('common.retry') }}</button>
      </p>
      <TestResultOutput v-else-if="preview" :result="preview" />
      <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-xs">
        <dt>{{ t('channelMonitorV2.intelligence.review') }}</dt>
        <dd>{{ t('channelMonitorV2.intelligence.art.' + target.artwork_status) }}</dd>
        <dt>{{ t('channelMonitorV2.intelligence.verdict') }}</dt>
        <dd>{{ t('channelMonitorV2.intelligence.states.' + target.verdict) }}</dd>
        <dt>{{ t('channelMonitorV2.intelligence.completed') }}</dt>
        <dd>{{ time(target.finished_at) }}</dd>
      </dl>
    </template>
  </dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import type { TestResult } from '@/types'
import {
  intelligenceArtwork,
  type IntelligenceGroup,
  type IntelligenceModel,
  type IntelligenceRound,
  type RoundAccount,
} from '@/api/intelligence'

const props = defineProps<{
  group: IntelligenceGroup
  round: IntelligenceRound
  accounts: RoundAccount[]
  initialAlias: string
  initialModel: IntelligenceModel
}>()
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const key = (m: IntelligenceModel) => JSON.stringify([m.model, m.effort])
const dialog = ref<HTMLDialogElement>()
const alias = ref(props.initialAlias)
const targetKey = ref(key(props.initialModel))
const account = computed(() => props.accounts.find((a) => a.alias === alias.value))
const target = computed(() => account.value?.models.find((m) => key(m) === targetKey.value) || account.value?.models[0])
const html = ref('')
const loading = ref(false)
const error = ref(false)
const retry = ref(0)
const preview = computed<TestResult | undefined>(() =>
  html.value && target.value?.artwork_id
    ? { id: target.value.artwork_id, output_kind: 'html', output_html: html.value, status: 'success' }
    : undefined,
)
const time = (v: string | null) => (v ? new Date(v).toLocaleString(locale.value) : '—')
watch(
  [target, () => props.group.id, retry],
  async ([m], _, cleanup) => {
    const request = new AbortController()
    cleanup(() => request.abort())
    html.value = ''
    error.value = false
    loading.value = Boolean(m?.artwork_id)
    if (!m?.artwork_id) return
    try {
      const result = await intelligenceArtwork(props.group.id, m.artwork_id, request.signal)
      if (!request.signal.aborted) {
        html.value = result
        error.value = !result
      }
    } catch {
      if (!request.signal.aborted) error.value = true
    } finally {
      if (!request.signal.aborted) loading.value = false
    }
  },
  { immediate: true },
)
onMounted(() => dialog.value?.showModal())
function onBackdrop(e: MouseEvent) {
  if (e.target !== dialog.value || !dialog.value) return
  const box = dialog.value.getBoundingClientRect()
  if (e.clientX < box.left || e.clientX > box.right || e.clientY < box.top || e.clientY > box.bottom) dialog.value.close()
}
</script>
