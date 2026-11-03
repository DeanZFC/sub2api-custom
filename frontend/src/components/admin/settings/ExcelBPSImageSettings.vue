<template>
        <div class="card" data-testid="excel-bps-image-settings">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('admin.settings.features.excelBpsImages.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.features.excelBpsImages.description') }}
            </p>
          </div>
          <form class="space-y-5 p-6" @submit.prevent="save">
            <div class="flex items-center justify-between gap-4">
              <div>
                <label for="excel-bps-image-enabled" class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.settings.features.excelBpsImages.enabled') }}
                </label>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.features.excelBpsImages.enabledHint') }}
                </p>
              </div>
              <Toggle id="excel-bps-image-enabled" v-model="form.excel_bps_image_relay_enabled" />
            </div>
            <div v-if="form.excel_bps_image_relay_enabled">
              <label for="excel-bps-image-mode" class="input-label">{{ t('admin.settings.features.excelBpsImages.mode') }}</label>
              <select id="excel-bps-image-mode" v-model="form.excel_bps_image_mode" class="input">
                <option value="relay">{{ t('admin.settings.features.excelBpsImages.modeRelay') }}</option>
                <option value="native">{{ t('admin.settings.features.excelBpsImages.modeNative') }}</option>
              </select>
              <p v-if="form.excel_bps_image_mode === 'native'" class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.features.excelBpsImages.nativeHint') }}
              </p>
              <div v-if="form.excel_bps_image_mode === 'relay'" class="mt-5">
                <label for="excel-bps-image-base-url" class="input-label">
                  {{ t('admin.settings.features.excelBpsImages.baseUrl') }}
                </label>
                <input
                  id="excel-bps-image-base-url"
                  v-model.trim="form.excel_bps_image_base_url"
                  type="url"
                  class="input"
                  placeholder="https://your-api.example.com"
                  required
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.features.excelBpsImages.baseUrlHint') }}
                </p>
              </div>
              <h4 class="mt-6 input-label">{{ t('admin.settings.features.excelBpsImages.requestLimitsTitle') }}</h4>
              <div class="mt-5 grid gap-4 sm:grid-cols-4">
                <div class="space-y-1">
                  <label for="excel-bps-image-body-limit" class="input-label">{{ t('admin.settings.features.excelBpsImages.bodyLimit') }}</label>
                  <input id="excel-bps-image-body-limit" v-model.number="form.excel_bps_image_body_limit_mib" class="input" type="number" min="1" max="128" step="1" required />
                </div>
                <div class="space-y-1">
                  <label for="excel-bps-image-budget" class="input-label">{{ t('admin.settings.features.excelBpsImages.budget') }}</label>
                  <input id="excel-bps-image-budget" v-model.number="form.excel_bps_image_budget_mib" class="input" type="number" min="512" max="2048" step="1" required />
                </div>
                <div class="space-y-1">
                  <label for="excel-bps-image-max-requests" class="input-label">{{ t('admin.settings.features.excelBpsImages.maxRequests') }}</label>
                  <input id="excel-bps-image-max-requests" v-model.number="form.excel_bps_image_max_requests" class="input" type="number" min="1" max="512" step="1" required />
                </div>
                <div class="space-y-1">
                  <label for="excel-bps-image-max-images" class="input-label">{{ t('admin.settings.features.excelBpsImages.maxImages') }}</label>
                  <input id="excel-bps-image-max-images" v-model.number="form.excel_bps_image_max_images" class="input" type="number" min="1" max="4096" step="1" required />
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.features.excelBpsImages.limitRange', { max: 4096 }) }}</p>
                </div>
              </div>
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.features.excelBpsImages.budgetHint') }}
              </p>
              <p v-if="form.excel_bps_image_mode === 'native'" class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.features.excelBpsImages.nativeRetentionHint', { maxImages: form.excel_bps_image_max_images }) }}
              </p>
              <template v-if="form.excel_bps_image_mode === 'relay'">
                <h4 class="mt-6 input-label">{{ t('admin.settings.features.excelBpsImages.imageLimitsTitle') }}</h4>
                <div class="mt-3 grid gap-4 sm:grid-cols-3">
                  <div class="space-y-1">
                    <label for="excel-bps-image-max-image-mib" class="input-label">{{ t('admin.settings.features.excelBpsImages.maxImageMiB') }}</label>
                    <input id="excel-bps-image-max-image-mib" v-model.number="form.excel_bps_image_max_image_mib" class="input" type="number" min="1" max="128" step="1" required />
                    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.features.excelBpsImages.limitRange', { max: 128 }) }}</p>
                  </div>
                  <div class="space-y-1">
                    <label for="excel-bps-image-max-total-mib" class="input-label">{{ t('admin.settings.features.excelBpsImages.maxTotalMiB') }}</label>
                    <input id="excel-bps-image-max-total-mib" v-model.number="form.excel_bps_image_max_total_mib" class="input" type="number" min="1" max="128" step="1" required />
                    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.features.excelBpsImages.limitRange', { max: 128 }) }}</p>
                  </div>
                  <div class="space-y-1">
                    <label for="excel-bps-image-storage-mib" class="input-label">{{ t('admin.settings.features.excelBpsImages.storageMiB') }}</label>
                    <input id="excel-bps-image-storage-mib" v-model.number="form.excel_bps_image_storage_mib" class="input" type="number" min="1" max="16384" step="1" required />
                    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.features.excelBpsImages.limitRange', { max: 16384 }) }}</p>
                  </div>
                  <div class="space-y-1">
                    <label for="excel-bps-image-storage-entries" class="input-label">{{ t('admin.settings.features.excelBpsImages.storageEntries') }}</label>
                    <input id="excel-bps-image-storage-entries" v-model.number="form.excel_bps_image_storage_entries" class="input" type="number" min="1" max="65536" step="1" required />
                    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.features.excelBpsImages.limitRange', { max: 65536 }) }}</p>
                  </div>
                  <div class="space-y-1">
                    <label for="excel-bps-image-ttl-minutes" class="input-label">{{ t('admin.settings.features.excelBpsImages.ttlMinutes') }}</label>
                    <input id="excel-bps-image-ttl-minutes" v-model.number="form.excel_bps_image_ttl_minutes" class="input" type="number" min="1" max="1440" step="1" required />
                    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.features.excelBpsImages.limitRange', { max: 1440 }) }}</p>
                  </div>
                </div>
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.features.excelBpsImages.retentionHint') }}
                </p>
              </template>
            </div>
            <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
            <button type="submit" class="btn btn-primary" :disabled="!loaded || saving">{{ t('common.save') }}</button>
          </form>
        </div>

</template>

<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { getSettings, updateSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const app = useAppStore()
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const form = reactive({
  excel_bps_image_mode: 'relay' as 'relay' | 'native', excel_bps_image_relay_enabled: false,
  excel_bps_image_base_url: '', excel_bps_image_body_limit_mib: 64,
  excel_bps_image_budget_mib: 1024, excel_bps_image_max_requests: 128,
  excel_bps_image_max_image_mib: 20, excel_bps_image_max_images: 20,
  excel_bps_image_max_total_mib: 32, excel_bps_image_storage_mib: 1024,
  excel_bps_image_storage_entries: 512, excel_bps_image_ttl_minutes: 30
})
onMounted(async () => {
  try {
    const settings = await getSettings()
    for (const key of Object.keys(form) as (keyof typeof form)[]) {
      const value = settings[key]
      if (value !== undefined) Object.assign(form, { [key]: value })
    }
    loaded.value = true
  } catch (cause) { error.value = (cause as Error).message }
})
async function save() {
  if (!loaded.value || saving.value) return
  error.value = ''
  try {
    const url = form.excel_bps_image_base_url.trim()
    if (url || (form.excel_bps_image_relay_enabled && form.excel_bps_image_mode === 'relay')) {
      const parsed = new URL(url)
      if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash) throw new Error(text('图片中转地址必须是无路径、无凭据的 HTTPS 站点地址。', 'Image relay URL must be an HTTPS origin without path or credentials.'))
      form.excel_bps_image_base_url = parsed.origin
    }
    const limits: [number, number, number][] = [
      [form.excel_bps_image_body_limit_mib, 1, 128], [form.excel_bps_image_budget_mib, 512, 2048],
      [form.excel_bps_image_max_requests, 1, 512], [form.excel_bps_image_max_image_mib, 1, 128],
      [form.excel_bps_image_max_images, 1, 4096], [form.excel_bps_image_max_total_mib, 1, 128],
      [form.excel_bps_image_storage_mib, 1, 16384], [form.excel_bps_image_storage_entries, 1, 65536],
      [form.excel_bps_image_ttl_minutes, 1, 1440]
    ]
    if (limits.some(([value, min, max]) => !Number.isInteger(value) || value < min || value > max)
      || form.excel_bps_image_budget_mib < form.excel_bps_image_body_limit_mib * 8
      || form.excel_bps_image_max_total_mib < form.excel_bps_image_max_image_mib
      || form.excel_bps_image_storage_mib < form.excel_bps_image_max_total_mib
      || form.excel_bps_image_storage_entries < form.excel_bps_image_max_images) throw new Error(text('图片限额不合法：预算至少是请求体上限的 8 倍，总量/存储和条目数须覆盖单次图片限制。', 'Invalid image limits: budget must cover eight request bodies, and storage must cover request image limits.'))
    saving.value = true
    await updateSettings({ ...form })
    app.showSuccess(t('common.saved'))
  } catch (cause) { error.value = (cause as Error).message }
  finally { saving.value = false }
}
</script>
