<template>
  <section class="card" data-testid="codex-ticket-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold">{{ t(`${prefix}.codexTicketEnabled`) }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t(`${prefix}.codexTicketEnabledDesc`) }}</p>
    </div>
    <form class="space-y-4 p-6" @submit.prevent="save">
      <fieldset :disabled="!loaded || saving" class="space-y-4">
        <label class="flex items-center justify-between text-sm"><span>{{ t(`${prefix}.codexTicketEnabled`) }}</span><input id="codex-ticket-enabled" v-model="form.openai_codex_ticket_enabled" type="checkbox" /></label>
        <label class="flex items-center justify-between text-sm"><span>{{ t(`${prefix}.codexTicketFailClosed`) }}</span><input id="codex-ticket-fail-closed" v-model="form.openai_codex_ticket_fail_closed" type="checkbox" /></label>
        <p class="text-xs text-gray-500">{{ t(`${prefix}.codexTicketFailClosedDesc`) }}</p>
        <div class="space-y-2">
          <label class="input-label">{{ t(`${prefix}.codexTicketModels`) }}</label>
          <ModelWhitelistSelector v-model="form.openai_codex_ticket_models" platform="openai" />
          <p class="input-hint">{{ t(`${prefix}.codexTicketShapeNotice`) }}</p>
        </div>
        <fieldset class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <legend class="px-1 text-sm font-semibold">{{ text('自动打票范围', 'Automatic harvest scope') }}</legend>
          <select id="codex-ticket-harvest-scope" v-model="form.openai_codex_ticket_harvest_scope.mode" class="input">
            <option value="all">{{ text('全部 OpenAI 账号', 'All OpenAI accounts') }}</option>
            <option value="selected">{{ text('仅指定分组', 'Selected groups only') }}</option>
          </select>
          <label class="input-label" for="codex-ticket-account-policy">{{ text('账号采集策略', 'Account harvest policy') }}</label>
          <select id="codex-ticket-account-policy" v-model="form.openai_codex_ticket_harvest_scope.account_policy" class="input">
            <option value="schedulable_only">{{ text('仅可调度账号', 'Schedulable accounts only') }}</option>
            <option value="prioritize_schedulable">{{ text('可调度优先，手动停调账号排队', 'Prioritize schedulable; queue manually disabled accounts') }}</option>
          </select>
          <div v-if="form.openai_codex_ticket_harvest_scope.mode === 'selected'" class="space-y-2">
            <p v-if="groupsError" class="text-sm text-amber-600">{{ text('分组加载失败，已选范围保留。', 'Group loading failed; saved selection is preserved.') }}</p>
            <label v-for="group in groupChoices" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="form.openai_codex_ticket_harvest_scope.group_ids" type="checkbox" :value="group.id" /><span>{{ group.name }} (#{{ group.id }})</span></label>
            <p v-if="!form.openai_codex_ticket_harvest_scope.group_ids.length" class="text-xs text-amber-600">{{ text('未选择分组时停止自动打票，不会退回全部账号。', 'An empty selection pauses harvesting; it never broadens to all accounts.') }}</p>
          </div>
          <p class="text-xs text-gray-500">{{ text('账号级「不打票」只停止采集。限流、冷却、过期和配额耗尽的账号始终跳过。保存后下轮生效。', 'Skip-harvest only stops collection. Rate-limited, cooling, expired and quota-exhausted accounts are skipped. Changes apply next round.') }}</p>
        </fieldset>
        <label class="block space-y-2 text-sm"><span>{{ text('票据刷新策略', 'Ticket refresh strategy') }}</span><select id="codex-ticket-strategy" v-model="form.openai_codex_ticket_strategy" class="input"><option value="standby">{{ text('提前准备备用', 'Prepare standby') }}</option><option value="fixed">{{ text('固定主用，失效后采集', 'Keep primary until invalid') }}</option></select></label>
        <label class="flex items-center gap-2 text-sm"><input id="codex-ticket-strict" v-model="form.openai_codex_ticket_strict_response" type="checkbox" />{{ text('严格拦截票据响应不匹配', 'Reject mismatched ticket response state') }}</label>
        <p class="text-xs text-gray-500">{{ text('响应头明确不匹配时拦截正文；上游可能已计费，不会自动重放。', 'Rejects the body on explicit state-header mismatch. The upstream may have charged; requests are not replayed.') }}</p>
        <div class="space-y-3">
          <label class="input-label" for="codex-ticket-proxy-mode">{{ t(`${prefix}.codexTicketHarvestProxy`) }}</label>
          <select id="codex-ticket-proxy-mode" v-model="proxyMode" class="input">
            <option value="static">{{ t(`${prefix}.codexTicketProxyModeStatic`) }}</option>
            <option value="mihomo">{{ t(`${prefix}.codexTicketProxyModeMihomo`) }}</option>
            <option value="ip_pool">{{ t(`${prefix}.codexTicketProxyModeIPPool`) }}</option>
          </select>
          <MihomoProxySelector v-if="proxyMode === 'mihomo'" @ready="mihomoReady = true" />
          <template v-else-if="proxyMode === 'static'">
            <input v-model.trim="staticProxy" type="text" class="input font-mono" :placeholder="t(`${prefix}.codexTicketHarvestProxyPlaceholder`)" autocomplete="off" />
            <p v-if="staticConfigured" class="text-xs text-gray-500">{{ t(`${prefix}.codexTicketHarvestProxyConfigured`) }}</p>
          </template>
          <p v-else class="text-xs text-gray-500">{{ t(`${prefix}.codexTicketProxyIPPoolHint`) }}</p>
        </div>
        <router-link to="/admin/harvest-flow" class="inline-block text-sm text-primary-600">{{ t('admin.harvestFlow.title') }} →</router-link>
        <div><button type="submit" class="btn btn-primary">{{ t('common.save') }}</button></div>
      </fieldset>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSettings, updateSettings } from '@/api/admin/settings'
import { getAll } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import MihomoProxySelector from '@/views/admin/settings/MihomoProxySelector.vue'

const prefix = 'admin.settings.gatewayForwarding'
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const app = useAppStore()
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const groupsError = ref(false)
const groups = ref<AdminGroup[]>([])
const proxyMode = ref<'static' | 'mihomo' | 'ip_pool'>('static')
const staticProxy = ref('')
const staticConfigured = ref(false)
const mihomoReady = ref(false)
const form = reactive({
  openai_codex_ticket_enabled: false,
  openai_codex_ticket_fail_closed: false,
  openai_codex_ticket_strategy: 'standby' as 'fixed' | 'standby',
  openai_codex_ticket_strict_response: false,
  openai_codex_ticket_models: ['gpt-6-astra', 'gpt-5.6-sol'],
  openai_codex_ticket_harvest_scope: { mode: 'all' as 'all' | 'selected', group_ids: [] as number[], account_policy: 'schedulable_only' as 'schedulable_only' | 'prioritize_schedulable' }
})
const groupChoices = computed(() => {
  const choices = groups.value.filter(group => group.platform === 'openai')
  for (const id of form.openai_codex_ticket_harvest_scope.group_ids) {
    if (!choices.some(group => group.id === id)) choices.push({ id, name: `#${id}` } as AdminGroup)
  }
  return choices
})
onMounted(async () => {
  void getAll().then(value => { groups.value = value }).catch(() => { groupsError.value = true })
  try {
    const data = await getSettings()
    for (const key of Object.keys(form) as (keyof typeof form)[]) {
      if (data[key] !== undefined) Object.assign(form, { [key]: data[key] })
    }
    const proxy = data.openai_codex_ticket_harvest_proxy_url || ''
    proxyMode.value = proxy === 'ippool://active' ? 'ip_pool' : proxy.replace(/\/+$/, '') === 'http://127.0.0.1:3101' ? 'mihomo' : 'static'
    mihomoReady.value = proxyMode.value === 'mihomo'
    staticConfigured.value = data.openai_codex_ticket_harvest_proxy_configured === true
    loaded.value = true
  } catch (cause) { error.value = (cause as Error).message }
})
async function save() {
  if (!loaded.value || saving.value) return
  error.value = ''
  if (proxyMode.value === 'mihomo' && !mihomoReady.value) {
    error.value = text('请先检测内核并点击设为打票代理。', 'Check the kernel and select it as the harvesting proxy first.')
    return
  }
  saving.value = true
  try {
    await updateSettings({ ...form,
      openai_codex_ticket_models: [...new Set(form.openai_codex_ticket_models.map(model => model.trim()).filter(Boolean))],
      openai_codex_ticket_harvest_scope: { ...form.openai_codex_ticket_harvest_scope, group_ids: [...new Set(form.openai_codex_ticket_harvest_scope.group_ids)] },
      openai_codex_ticket_harvest_proxy_url: proxyMode.value === 'mihomo' ? 'http://127.0.0.1:3101' : proxyMode.value === 'ip_pool' ? 'ippool://active' : staticProxy.value,
      openai_codex_ticket_use_saved_static_proxy: proxyMode.value === 'static' && !staticProxy.value
    })
    app.showSuccess(t('common.saved'))
  } catch (cause) { error.value = (cause as Error).message }
  finally { saving.value = false }
}
</script>
