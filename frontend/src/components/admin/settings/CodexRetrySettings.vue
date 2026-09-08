<template>
  <section class="space-y-4 border-b border-gray-100 pb-6 dark:border-dark-700" data-testid="codex-retry-settings">
    <div class="flex items-center justify-between gap-4">
      <h3 id="codex-retry-title" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.gatewayForwarding.codexRetry.title') }}</h3>
      <Toggle v-model="form.enabled" aria-labelledby="codex-retry-title" :disabled="loading || saving || !loaded" />
    </div>
    <div v-if="loading" class="flex h-20 items-center justify-center" role="status">
      <Icon name="refresh" class="animate-spin text-gray-400" />
    </div>
    <div v-else-if="!loaded" class="flex items-center gap-3 text-sm text-red-600" role="alert">
      {{ t('admin.settings.gatewayForwarding.codexRetry.loadFailed') }}
      <button type="button" class="btn btn-secondary btn-sm" :title="t('common.retry')" @click="load"><Icon name="refresh" size="sm" /></button>
    </div>
    <template v-else>
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayForwarding.codexRetry.resilienceHint') }}</p>
      <fieldset :disabled="saving || !form.enabled" class="grid min-w-0 gap-4 sm:grid-cols-3 disabled:opacity-60">
        <div>
          <label for="codex-retry-count" class="input-label">{{ t('admin.settings.gatewayForwarding.codexRetry.maxRetries') }}</label>
          <input id="codex-retry-count" v-model.number="form.max_retries" type="number" min="1" max="10" step="1" class="input w-full" />
        </div>
        <div>
          <label for="codex-retry-interval" class="input-label">{{ t('admin.settings.gatewayForwarding.codexRetry.interval') }}</label>
          <input id="codex-retry-interval" v-model.number="form.retry_interval_ms" type="number" min="100" max="10000" step="100" class="input w-full" />
        </div>
        <div>
          <label for="codex-retry-window" class="input-label">{{ t('admin.settings.gatewayForwarding.codexRetry.window') }}</label>
          <input id="codex-retry-window" v-model.number="form.max_retry_window_seconds" type="number" min="1" max="300" step="1" class="input w-full" />
        </div>
        <div class="flex items-center justify-between gap-4 sm:col-span-3">
          <div>
            <label for="codex-retry-backoff" class="input-label">{{ t('admin.settings.gatewayForwarding.codexRetry.backoff') }}</label>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayForwarding.codexRetry.backoffHint') }}</p>
          </div>
          <input id="codex-retry-backoff" v-model="form.exponential_backoff" type="checkbox" class="h-4 w-4 shrink-0" />
        </div>
        <div class="flex items-center justify-between gap-4 sm:col-span-3">
          <div>
            <label for="codex-retry-buffer" class="input-label">{{ t('admin.settings.gatewayForwarding.codexRetry.buffer') }}</label>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayForwarding.codexRetry.bufferHint') }}</p>
          </div>
          <input id="codex-retry-buffer" v-model="form.buffer_until_complete" type="checkbox" class="h-4 w-4 shrink-0" />
        </div>
        <div class="min-w-0 sm:col-span-3">
          <label for="codex-retry-keywords" class="input-label">{{ t('admin.settings.gatewayForwarding.codexRetry.keywords') }}</label>
          <textarea id="codex-retry-keywords" v-model="keywordText" rows="4" class="input w-full font-mono text-sm" spellcheck="false" />
        </div>
      </fieldset>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span v-if="validationError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ validationError }}</span>
        <button type="button" class="btn btn-primary btn-sm ml-auto" :disabled="saving || !!validationError" data-testid="codex-retry-save" @click="save">
          <Icon :name="saving ? 'refresh' : 'check'" size="sm" class="mr-1.5" :class="{ 'animate-spin': saving }" />
          {{ t('admin.settings.gatewayForwarding.codexRetry.save') }}
        </button>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { CodexPreOutputRetrySettings } from '@/api/admin/settings'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const keywordText = ref('')
const form = reactive<CodexPreOutputRetrySettings>({
  exponential_backoff: false, buffer_until_complete: false,
  enabled: false, max_retries: 3, retry_interval_ms: 1000, max_retry_window_seconds: 30, keywords: [],
})
const keywords = computed(() => keywordText.value.split('\n').map(value => value.trim()).filter(Boolean))
const validationError = computed(() => {
  if (!Number.isInteger(form.max_retries) || form.max_retries < 1 || form.max_retries > 10 ||
      !Number.isInteger(form.retry_interval_ms) || form.retry_interval_ms < 100 || form.retry_interval_ms > 10000 ||
      !Number.isInteger(form.max_retry_window_seconds) || form.max_retry_window_seconds < 1 || form.max_retry_window_seconds > 300) {
    return t('admin.settings.gatewayForwarding.codexRetry.invalidLimits')
  }
  if ((form.enabled && !keywords.value.length) || keywords.value.length > 50 || keywords.value.some(value => new TextEncoder().encode(value).length > 256)) {
    return t('admin.settings.gatewayForwarding.codexRetry.invalidKeywords')
  }
  return ''
})

function apply(settings: CodexPreOutputRetrySettings) {
  Object.assign(form, { exponential_backoff: false, buffer_until_complete: false }, settings)
  keywordText.value = settings.keywords.join('\n')
}

async function load() {
  loading.value = true
  try {
    apply(await adminAPI.settings.getCodexPreOutputRetrySettings())
    loaded.value = true
  } catch {
    loaded.value = false
  } finally {
    loading.value = false
  }
}

async function save() {
  if (validationError.value || saving.value || !loaded.value) return
  saving.value = true
  try {
    apply(await adminAPI.settings.updateCodexPreOutputRetrySettings({ ...form, keywords: keywords.value }))
    appStore.showSuccess(t('admin.settings.gatewayForwarding.codexRetry.saved'))
  } catch {
    appStore.showError(t('admin.settings.gatewayForwarding.codexRetry.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
