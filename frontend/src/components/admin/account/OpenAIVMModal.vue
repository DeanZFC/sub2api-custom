<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.openaiVm.title')"
    width="wide"
    :close-on-escape="!busy"
    :show-close-button="!busy"
    @close="close"
  >
    <div class="space-y-5" :aria-busy="busy">
      <div class="rounded-lg bg-sky-50 p-3 text-sm text-sky-900 dark:bg-sky-900/20 dark:text-sky-200">
        <p class="font-medium">{{ account?.name }}</p>
        <p class="mt-1">{{ t('admin.accounts.openaiVm.description') }}</p>
        <p class="mt-1">{{ t('admin.accounts.openaiVm.engineHint') }}</p>
      </div>

      <p v-if="operation" role="status" class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
        <Icon name="refresh" size="sm" class="animate-spin" />
        {{ t('admin.accounts.openaiVm.working') }}
      </p>
      <p v-if="error" role="alert" class="whitespace-pre-wrap break-words rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">{{ notice }}</p>

      <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
        <h4 class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.openaiVm.configTitle') }}</h4>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.configHint') }}</p>
        <p v-if="configError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ configError }}</p>
        <button v-if="!config && !busy" type="button" class="btn btn-secondary btn-sm" @click="load">{{ t('common.retry') }}</button>
        <form v-if="config" data-testid="vm-config-form" @submit.prevent="save">
          <fieldset :disabled="busy" class="space-y-3 disabled:opacity-70">
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
              <input v-model="form.enabled" type="checkbox" data-testid="vm-enabled" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
              {{ t('admin.accounts.openaiVm.enabled') }}
            </label>
            <div>
              <label for="openai-vm-base-url" class="input-label">{{ t('admin.accounts.openaiVm.baseUrl') }}</label>
              <input id="openai-vm-base-url" v-model="form.base_url" type="url" :required="form.enabled" class="input" placeholder="http://127.0.0.1:8097" autocomplete="off" />
            </div>
            <div>
              <label for="openai-vm-proxy-url" class="input-label">{{ t('admin.accounts.openaiVm.proxyUrl') }}</label>
              <input id="openai-vm-proxy-url" v-model="form.proxy_url" type="url" :required="form.enabled" class="input" placeholder="http://127.0.0.1:8097" autocomplete="off" />
            </div>
            <div>
              <label for="openai-vm-secret" class="input-label">{{ t('admin.accounts.openaiVm.secret') }}</label>
              <input id="openai-vm-secret" v-model="form.secret" type="password" class="input" autocomplete="new-password" :placeholder="t(config.secret_configured ? 'admin.accounts.openaiVm.secretKeep' : 'admin.accounts.openaiVm.secretMissing')" />
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(config.secret_configured ? 'admin.accounts.openaiVm.secretConfigured' : 'admin.accounts.openaiVm.secretMissing') }}</p>
            </div>
            <div class="flex flex-wrap gap-2">
              <button type="submit" class="btn btn-primary btn-sm" data-testid="vm-save">{{ t('common.save') }}</button>
              <button type="button" :disabled="configDirty || !config.enabled || !config.secret_configured" class="btn btn-secondary btn-sm" data-testid="vm-test" @click="test">{{ t('admin.accounts.openaiVm.test') }}</button>
            </div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(configDirty ? 'admin.accounts.openaiVm.saveFirst' : 'admin.accounts.openaiVm.testHint') }}</p>
          </fieldset>
        </form>
      </section>

      <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
        <div class="flex items-center justify-between gap-3">
          <h4 class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.openaiVm.bindingTitle') }}</h4>
          <button type="button" :disabled="busy" class="btn btn-secondary btn-sm" data-testid="vm-refresh" @click="refresh">{{ t('common.refresh') }}</button>
        </div>
        <p v-if="bindingError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ bindingError }}</p>
        <p v-if="state && !eligible" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.accounts.openaiVm.ineligible') }}</p>
        <template v-if="binding">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.slotId') }}</dt>
            <dd class="break-all font-mono text-gray-900 dark:text-white">{{ binding.slot_id || '—' }}</dd>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('common.status') }}</dt>
            <dd :class="binding.status === 'ready' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'" data-testid="vm-binding-status">{{ statusLabel }}</dd>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.updatedAt') }}</dt>
            <dd class="break-all text-gray-900 dark:text-white">{{ binding.updated_at }}</dd>
          </dl>
          <p v-if="binding.last_error" role="alert" class="whitespace-pre-wrap break-words rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ binding.last_error }}</p>
          <p v-if="binding.status !== 'ready'" class="text-xs text-amber-600 dark:text-amber-400">{{ t('admin.accounts.openaiVm.notReady') }}</p>
          <div v-if="eligible" class="flex flex-wrap gap-2">
            <button v-if="canRetry" type="button" :disabled="busy || (configDirty && !cleanupRetry)" class="btn btn-primary btn-sm" data-testid="vm-retry" @click="mutate('retry')">{{ t(cleanupRetry ? 'admin.accounts.openaiVm.retryCleanup' : 'admin.accounts.openaiVm.retry') }}</button>
            <button type="button" :disabled="busy" class="btn btn-danger btn-sm" data-testid="vm-delete" @click="confirmDelete = true">{{ t('admin.accounts.openaiVm.unbind') }}</button>
          </div>
        </template>
        <template v-else-if="state">
          <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.openaiVm.unbound') }}</p>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.proxyHint') }}</p>
          <p v-if="!state.configured" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.accounts.openaiVm.notConfigured') }}</p>
          <div v-if="eligible" class="flex flex-wrap items-end gap-3">
            <div>
              <label for="openai-vm-runtime" class="input-label">{{ t('admin.accounts.openaiVm.runtime') }}</label>
              <select id="openai-vm-runtime" v-model="runtime" class="input" :disabled="busy">
                <option value="docker">Docker</option>
                <option value="kvm">KVM</option>
              </select>
            </div>
            <button type="button" :disabled="busy || !state.configured || !config?.enabled || configDirty" class="btn btn-primary" data-testid="vm-create" @click="mutate('create')">{{ t('admin.accounts.openaiVm.create') }}</button>
          </div>
        </template>
        <div v-if="confirmDelete" class="space-y-3 rounded-lg border border-red-200 bg-red-50 p-3 dark:border-red-900 dark:bg-red-900/20">
          <p class="text-sm text-red-700 dark:text-red-300">{{ t('admin.accounts.openaiVm.unbindConfirm') }}</p>
          <div class="flex gap-2">
            <button type="button" :disabled="busy" class="btn btn-danger btn-sm" data-testid="vm-delete-confirm" @click="mutate('delete')">{{ t('admin.accounts.openaiVm.unbind') }}</button>
            <button type="button" :disabled="busy" class="btn btn-secondary btn-sm" @click="confirmDelete = false">{{ t('common.cancel') }}</button>
          </div>
        </div>
      </section>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { Icon } from '@/components/icons'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import { openaiVmAPI, type OpenAIVMAccountState, type OpenAIVMBinding, type OpenAIVMConfig, type OpenAIVMRuntime } from '@/api/admin/openaiVm'
import type { Account } from '@/types'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'updated'): void }>()
const { t } = useI18n()
const stepUp = useStepUp()
const config = ref<OpenAIVMConfig | null>(null)
const form = reactive({ enabled: false, base_url: '', proxy_url: '', secret: '' })
const state = ref<OpenAIVMAccountState | null>(null)
const runtime = ref<OpenAIVMRuntime>('docker')
const operation = ref('')
const error = ref('')
const notice = ref('')
const configError = ref('')
const bindingError = ref('')
const confirmDelete = ref(false)
let session = 0

const busy = computed(() => operation.value !== '')
const binding = computed(() => state.value?.binding ?? null)
const eligible = computed(() => Boolean(state.value?.eligible && props.account?.platform === 'openai' && props.account.type === 'oauth' && props.account.parent_account_id == null))
const configDirty = computed(() => !config.value || form.enabled !== config.value.enabled || form.base_url.trim() !== config.value.base_url || form.proxy_url.trim() !== config.value.proxy_url || form.secret !== '')
const cleanupRetry = computed(() => ['deleting', 'delete_failed'].includes(binding.value?.status ?? ''))
const canRetry = computed(() => ['provisioning', 'error'].includes(binding.value?.status ?? '') || cleanupRetry.value)
const statusLabel = computed(() => {
  const status = binding.value?.status ?? ''
  return ['provisioning', 'ready', 'error', 'deleting', 'delete_failed'].includes(status)
    ? t(`admin.accounts.openaiVm.status.${status}`)
    : status
})

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

async function readBinding(accountId: number, current: number) {
  const value = await openaiVmAPI.getBinding(accountId)
  if (current !== session) return
  state.value = value
  bindingError.value = ''
}

async function load() {
  if (busy.value || !props.show || !props.account) return
  const current = session
  const accountId = props.account.id
  operation.value = 'load'
  const [configResult, bindingResult] = await Promise.allSettled([openaiVmAPI.getConfig(), openaiVmAPI.getBinding(accountId)])
  if (current !== session) return
  if (configResult.status === 'fulfilled') {
    applyConfig(configResult.value)
    configError.value = ''
  } else configError.value = messageOf(configResult.reason)
  if (bindingResult.status === 'fulfilled') {
    state.value = bindingResult.value
    bindingError.value = ''
  } else bindingError.value = messageOf(bindingResult.reason)
  operation.value = ''
}

async function run(kind: string, action: (accountId: number, current: number) => Promise<void>) {
  if (busy.value || !props.account || !props.show) return
  const current = session
  const accountId = props.account.id
  operation.value = kind
  notice.value = ''
  try {
    await action(accountId, current)
    if (current === session) error.value = ''
  } catch (cause) {
    if (current !== session || isStepUpCancelled(cause)) return
    error.value = messageOf(cause)
    // A timeout can still leave a server-side binding. Refresh without hiding the error.
    if (['create', 'retry', 'delete'].includes(kind)) {
      try { await readBinding(accountId, current) } catch (refreshError) {
        if (current === session) bindingError.value = messageOf(refreshError)
      }
      if (current === session) emit('updated')
    }
  } finally {
    if (current === session) operation.value = ''
  }
}

async function save() {
  if (!config.value) return
  const payload = { enabled: form.enabled, base_url: form.base_url.trim(), proxy_url: form.proxy_url.trim(), ...(form.secret ? { secret: form.secret } : {}) }
  await run('save', async (accountId, current) => {
    const saved = await stepUp.run(() => openaiVmAPI.saveConfig(payload))
    if (current !== session) return
    applyConfig(saved)
    configError.value = ''
    await readBinding(accountId, current)
    if (current === session) notice.value = t('admin.accounts.openaiVm.saved')
  })
}

async function test() {
  if (configDirty.value || !config.value?.enabled || !config.value.secret_configured) return
  await run('test', async (_accountId, current) => {
    const result = await stepUp.run(() => openaiVmAPI.testConnection())
    if (!result.healthy) throw new Error(result.message || t('admin.accounts.openaiVm.failed'))
    if (current === session) notice.value = t('admin.accounts.openaiVm.healthy')
  })
}

async function refresh() {
  await run('refresh', readBinding)
}

async function mutate(kind: 'create' | 'retry' | 'delete') {
  if (!eligible.value) return
  if (kind === 'create' && (binding.value || !state.value?.configured || !config.value?.enabled || configDirty.value)) return
  if (kind === 'retry' && (!canRetry.value || (configDirty.value && !cleanupRetry.value))) return
  if (kind === 'delete' && (!binding.value || !confirmDelete.value)) return
  const runtimeType = runtime.value
  const cleaningUp = kind === 'delete' || (kind === 'retry' && cleanupRetry.value)
  await run(kind, async (accountId, current) => {
    const result = await stepUp.run<OpenAIVMBinding | null | void>(() => kind === 'create'
      ? openaiVmAPI.createBinding(accountId, runtimeType)
      : kind === 'retry'
        ? openaiVmAPI.retryBinding(accountId)
        : openaiVmAPI.deleteBinding(accountId))
    if (current !== session) return
    if (state.value) state.value = { ...state.value, binding: result ?? null }
    confirmDelete.value = false
    await readBinding(accountId, current)
    if (current !== session) return
    if (cleaningUp) {
      if (binding.value) throw new Error(t('admin.accounts.openaiVm.cleanupUnconfirmed'))
      notice.value = t('admin.accounts.openaiVm.unboundSuccess')
    } else {
      if (!binding.value) throw new Error(t('admin.accounts.openaiVm.createUnconfirmed'))
      if (binding.value.status === 'ready') notice.value = t('admin.accounts.openaiVm.readySuccess')
    }
    emit('updated')
  })
}

function close() {
  if (!busy.value) emit('close')
}

watch([() => props.show, () => props.account?.id], () => {
  session += 1
  stepUp.onCancel()
  operation.value = ''
  config.value = null
  state.value = null
  form.secret = ''
  error.value = ''
  notice.value = ''
  configError.value = ''
  bindingError.value = ''
  confirmDelete.value = false
  runtime.value = 'docker'
  if (props.show && props.account) void load()
}, { immediate: true })

onUnmounted(() => { session += 1; stepUp.onCancel() })
</script>
