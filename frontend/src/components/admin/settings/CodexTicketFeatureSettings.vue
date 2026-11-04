<template>
  <section class="card p-6" data-testid="codex-ticket-feature-settings">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h3 class="text-lg font-semibold">{{ text('Codex 打票功能', 'Codex ticket harvesting') }}</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ text('默认关闭。开启后显示打票管理、自动策略、号池调度和采票出口等入口，并启用打票运行。', 'Off by default. Enable ticket management, harvest policies, pool scheduling, and harvest exit controls, and start ticket harvesting.') }}</p>
      </div>
      <Toggle id="codex-ticket-feature-enabled" v-model="enabled" :disabled="!loaded || saving" />
    </div>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
    <button type="button" class="btn btn-primary mt-4" :disabled="!loaded || saving" @click="save">{{ t('common.save') }}</button>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { getSettings, updateSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const app = useAppStore()
const enabled = ref(false)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
onMounted(async () => {
  try {
    enabled.value = (await getSettings()).openai_codex_ticket_enabled === true
    loaded.value = true
  } catch {
    error.value = text('无法读取打票设置，请刷新重试。', 'Could not load ticket settings. Refresh and try again.')
  }
})
async function save() {
  if (!loaded.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    const saved = await updateSettings({ openai_codex_ticket_enabled: enabled.value })
    enabled.value = saved.openai_codex_ticket_enabled === true
    if (app.cachedPublicSettings) {
      app.cachedPublicSettings = { ...app.cachedPublicSettings, openai_codex_ticket_enabled: enabled.value }
    }
    await app.fetchPublicSettings(true)
    app.showSuccess(t('common.saved'))
  } catch {
    error.value = text('打票设置未保存，请重试。', 'Ticket settings were not saved. Please try again.')
  } finally {
    saving.value = false
  }
}
</script>
