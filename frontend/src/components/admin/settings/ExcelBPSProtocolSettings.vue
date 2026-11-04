<template>
  <section class="card p-6" data-testid="excel-bps-protocol-settings">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h3 class="text-lg font-semibold">Excel / BPS</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ text('控制全站 Excel / BPS 协议。账号仍需独立开启协议并完成 Excel 授权。', 'Control Excel / BPS for this site. Each account still needs its protocol enabled and a valid Excel authorization.') }}</p>
      </div>
      <Toggle id="excel-bps-protocol-enabled" v-model="enabled" :disabled="!loaded || saving" />
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
const enabled = ref(true)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
onMounted(async () => {
  try {
    enabled.value = (await getSettings()).excel_bps_enabled !== false
    loaded.value = true
  } catch {
    error.value = text('无法读取协议设置，请刷新重试。', 'Could not load protocol settings. Refresh and try again.')
  }
})
async function save() {
  if (!loaded.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    await updateSettings({ excel_bps_enabled: enabled.value })
    await app.fetchPublicSettings(true)
    app.showSuccess(t('common.saved'))
  } catch {
    error.value = text('协议设置未保存，请重试。', 'Protocol settings were not saved. Please try again.')
  } finally {
    saving.value = false
  }
}
</script>
