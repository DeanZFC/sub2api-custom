<template>
  <AppLayout>
    <nav v-if="traffic && directory.groups.length" class="tabs mb-4 inline-flex" :aria-label="t('nav.channelStatus')">
      <RouterLink
        class="tab"
        :class="{ 'tab-active': !quality }"
        :to="{ path: '/monitor', query: { ...route.query, section: undefined } }"
        >{{ t('nav.channelStatus') }}</RouterLink
      >
      <RouterLink
        class="tab"
        :class="{ 'tab-active': quality }"
        :to="{ path: '/monitor', query: { ...route.query, section: 'intelligence' } }"
        >{{ tr(directory.roundsVisible ? 'monitor' : 'legacy') }}</RouterLink
      >
    </nav>
    <template v-if="quality">
      <header class="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h1 class="page-title">{{ tr(directory.roundsVisible ? 'monitor' : 'legacy') }}</h1>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="directory.loading" @click="directory.load(true)">
          {{ t('common.refresh') }}
        </button>
      </header>
      <p v-if="directory.failed" role="alert">{{ tr('loadError') }}</p>
      <p v-else-if="!directory.groups.length" class="text-sm text-gray-500">
        {{ directory.loading ? t('common.loading') : tr('noGroups') }}
      </p>
      <template v-if="standard.length">
        <div class="mb-3 flex flex-wrap items-center gap-3">
          <label class="text-sm"
            >{{ tr('group') }}
            <select class="input" :value="groupFilter" @change="selectGroup(($event.target as HTMLSelectElement).value)">
              <option value="">{{ t('channelMonitorV2.filters.allGroups') }}</option>
              <option v-for="g in standard" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
          </label>
          <label class="text-sm"
            >{{ t('tests.modelFilter') }}
            <select class="input" :value="model" @change="selectModel(($event.target as HTMLSelectElement).value)">
              <option value="">{{ t('channelMonitorV2.filters.allModels') }}</option>
              <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
            </select>
          </label>
        </div>
        <div class="grid items-start gap-4 lg:grid-cols-2">
          <article v-for="g in scopedGroups" :key="g.id" class="card min-w-0 p-4">
            <h2 class="font-semibold">{{ g.name }}</h2>
            <IntelligenceRounds :group="g" :model="model || undefined" :refresh-key="directory.refreshed" />
          </article>
        </div>
      </template>
      <details
        v-if="directory.groups.some((g) => g.has_legacy_results)"
        class="card mt-4 p-4"
        @toggle="openLegacy = ($event.target as HTMLDetailsElement).open"
      >
        <summary class="cursor-pointer font-medium">{{ tr('legacy') }}</summary>
        <LegacyResults v-if="openLegacy" embedded class="mt-4" />
      </details>
    </template>
    <ChannelStatusV1View v-else-if="traffic && isV1" />
    <ChannelStatusV2View v-else-if="traffic" />
    <p v-else class="card p-5 text-sm text-gray-500" role="status">
      {{ directory.loading ? t('common.loading') : directory.failed ? tr('loadError') : tr('noGroups') }}
      <button v-if="directory.failed" type="button" class="ml-2 underline" @click="directory.load(true)">{{ t('common.retry') }}</button>
    </p>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import IntelligenceRounds from '@/features/channel-monitor-v2/IntelligenceRounds.vue'
import { useIntelligenceGroupsStore } from '@/stores/intelligenceGroups'
import { useAuthStore } from '@/stores/auth'
import { isChannelMonitorRouteEnabled, isChannelMonitorV1Mode } from '@/utils/featureFlags'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'

const LegacyResults = defineAsyncComponent(() => import('./TestResultsView.vue'))
const directory = useIntelligenceGroupsStore()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const tr = (k: string) => t('channelMonitorV2.intelligence.' + k)
const isV1 = computed(isChannelMonitorV1Mode)
const traffic = computed(isChannelMonitorRouteEnabled)
const quality = computed(() => (directory.roundsVisible || directory.groups.some(g => g.has_legacy_results))
  && (!traffic.value || route.query.section === 'intelligence'))
const standard = computed(() => directory.roundsVisible ? directory.groups.filter((g) => g.models.length) : [])
const groupFilter = computed(() => {
  const selected = standard.value.find((g) => String(g.id) === route.query.group)
  return selected ? String(selected.id) : ''
})
const selectedGroups = computed(() => standard.value.filter((g) => !groupFilter.value || groupFilter.value === String(g.id)))
const models = computed(() => [...new Set(selectedGroups.value.flatMap((g) => g.models))].sort())
const model = computed(() =>
  typeof route.query.model === 'string' && models.value.includes(route.query.model) ? route.query.model : '',
)
const scopedGroups = computed(() => selectedGroups.value.filter((g) => !model.value || g.models.includes(model.value)))
const openLegacy = ref(false)
function selectGroup(id: string) {
  void router.replace({ query: { ...route.query, group: id || undefined, model: undefined } })
}
function selectModel(value: string) {
  void router.replace({ query: { ...route.query, model: value || undefined } })
}
watch(
  () => auth.user?.id,
  () => {
    openLegacy.value = false
    void directory.load(true)
  },
  { immediate: true },
)
let timer: ReturnType<typeof setInterval>
function recheckVisibility() {
  if (!document.hidden) void directory.load(true)
}
onMounted(() => {
  document.addEventListener('visibilitychange', recheckVisibility)
  timer = setInterval(() => {
    if (!document.hidden) void directory.load()
  }, 60_000)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', recheckVisibility)
})
</script>
