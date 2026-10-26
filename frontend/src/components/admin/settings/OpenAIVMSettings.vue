<template>
  <section class="card" data-testid="openai-vm-settings" :aria-busy="busy">
    <div class="border-b border-gray-100 p-6 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.openaiVm.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.openaiVm.description') }}</p>
    </div>
    <div class="space-y-5 p-6">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.configHint') }}</p>
      <p v-if="busy" role="status" class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
        <Icon name="refresh" size="sm" class="animate-spin" />
        {{ t('admin.accounts.openaiVm.working') }}
      </p>
      <p v-if="error" role="alert" class="whitespace-pre-wrap break-words rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">{{ notice }}</p>
      <button v-if="!config && !busy" type="button" class="btn btn-secondary btn-sm" data-testid="vm-config-retry" @click="load">{{ t('common.retry') }}</button>
      <form v-if="config" data-testid="vm-config-form" @submit.prevent="save">
        <fieldset :disabled="busy" class="space-y-5 disabled:opacity-70">
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
            <input v-model="form.enabled" type="checkbox" data-testid="vm-enabled" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
            {{ t('admin.accounts.openaiVm.enabled') }}
          </label>
          <div class="grid gap-5 md:grid-cols-2">
            <div>
              <label for="openai-vm-base-url" class="input-label">{{ t('admin.accounts.openaiVm.baseUrl') }}</label>
              <input id="openai-vm-base-url" v-model="form.base_url" type="url" :required="form.enabled" class="input" placeholder="http://127.0.0.1:8097" autocomplete="off" />
            </div>
            <div>
              <label for="openai-vm-proxy-url" class="input-label">{{ t('admin.accounts.openaiVm.proxyUrl') }}</label>
              <input id="openai-vm-proxy-url" v-model="form.proxy_url" type="url" :required="form.enabled" class="input" placeholder="http://127.0.0.1:8097" autocomplete="off" />
            </div>
          </div>
          <div>
            <label for="openai-vm-secret" class="input-label">{{ t('admin.accounts.openaiVm.secret') }}</label>
            <input id="openai-vm-secret" v-model="form.secret" type="password" class="input" autocomplete="new-password" :placeholder="t(config.secret_configured ? 'admin.accounts.openaiVm.secretKeep' : 'admin.accounts.openaiVm.secretMissing')" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(config.secret_configured ? 'admin.accounts.openaiVm.secretConfigured' : 'admin.accounts.openaiVm.secretMissing') }}</p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="submit" :disabled="busy" class="btn btn-primary btn-sm" data-testid="vm-save">{{ t('common.save') }}</button>
            <button type="button" :disabled="busy || configDirty || !config.enabled || !config.secret_configured" class="btn btn-secondary btn-sm" data-testid="vm-test" @click="test">{{ t('admin.accounts.openaiVm.test') }}</button>
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(configDirty ? 'admin.accounts.openaiVm.saveFirst' : 'admin.accounts.openaiVm.testHint') }}</p>
        </fieldset>
      </form>
    </div>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { Icon } from '@/components/icons'
import { isStepUpBlocked, isStepUpCancelled, StepUpCancelledError, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import { openaiVmAPI, type OpenAIVMConfig } from '@/api/admin/openaiVm'

const { t } = useI18n()
const stepUp = useStepUp()
const config = ref<OpenAIVMConfig | null>(null)
const form = reactive({ enabled: false, base_url: '', proxy_url: '', secret: '' })
const operation = ref('')
const error = ref('')
const notice = ref('')
let disposed = false

const busy = computed(() => operation.value !== '')
const configDirty = computed(() => !config.value || form.enabled !== config.value.enabled || form.base_url.trim() !== config.value.base_url || form.proxy_url.trim() !== config.value.proxy_url || form.secret !== '')

function messageOf(cause: unknown): string {
  if (isStepUpBlocked(cause)) {
    return t(stepUpBlockReason(cause) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
  }
  const message = (cause as { message?: unknown } | null)?.message
  return typeof message === 'string' && message ? message : t('admin.accounts.openaiVm.failed')
}

function applyConfig(value: OpenAIVMConfig) {
  config.value = value
  Object.assign(form, { enabled: value.enabled, base_url: value.base_url, proxy_url: value.proxy_url, secret: '' })
}

async function run(kind: string, action: () => Promise<void>) {
  if (disposed || busy.value) return
  operation.value = kind
  notice.value = ''
  try {
    await action()
    if (!disposed) error.value = ''
  } catch (cause) {
    if (!disposed && !isStepUpCancelled(cause)) error.value = messageOf(cause)
  } finally {
    if (!disposed) operation.value = ''
  }
}

async function withStepUp<T>(action: () => Promise<T>): Promise<T> {
  return stepUp.run(async () => {
    if (disposed) throw new StepUpCancelledError()
    try {
      const result = await action()
      if (disposed) throw new StepUpCancelledError()
      return result
    } catch (cause) {
      // A late response must not reopen verification after navigating away.
      if (disposed) throw new StepUpCancelledError()
      throw cause
    }
  })
}

async function load() {
  await run('load', async () => {
    const value = await openaiVmAPI.getConfig()
    if (!disposed) applyConfig(value)
  })
}

async function save() {
  if (!config.value) return
  const payload = { enabled: form.enabled, base_url: form.base_url.trim(), proxy_url: form.proxy_url.trim(), ...(form.secret ? { secret: form.secret } : {}) }
  await run('save', async () => {
    const saved = await withStepUp(() => openaiVmAPI.saveConfig(payload))
    if (disposed) return
    applyConfig(saved)
    notice.value = t('admin.accounts.openaiVm.saved')
  })
}

async function test() {
  if (configDirty.value || !config.value?.enabled || !config.value.secret_configured) return
  await run('test', async () => {
    const result = await withStepUp(() => openaiVmAPI.testConnection())
    if (disposed) return
    if (!result.healthy) throw new Error(result.message || t('admin.accounts.openaiVm.failed'))
    notice.value = t('admin.accounts.openaiVm.healthy')
  })
}

onMounted(load)
onBeforeUnmount(() => {
  disposed = true
  form.secret = ''
  stepUp.onCancel()
})
</script>
