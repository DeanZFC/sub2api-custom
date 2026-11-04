<template>
  <section class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="excel-authorization-panel">
    <button type="button" class="text-sm font-medium text-primary-600" :aria-expanded="expanded" @click="toggle">
      {{ text('BPS 独立授权与授权材料', 'BPS authorization and login credentials') }}
    </button>
    <div v-if="expanded" class="mt-3 space-y-3 text-sm">
      <p class="text-gray-500">{{ text('已有 Codex 账号可在此取得独立 Excel 授权。自动授权需要运行授权 Worker；也可通过新增 Excel OAuth 账号手动授权。', 'Obtain separate Excel authorization for an existing Codex account. Automatic authorization requires an authorization worker. Manual authorization is available when creating an Excel OAuth account.') }}</p>
      <ExcelBPSCredentialStatus :state="account.excel_bps_credential_state" />
      <p v-if="runtime">{{ text('授权服务状态', 'Authorization service') }}: {{ runtime.state }}</p>
      <p v-if="status.task" role="status">{{ text('最近任务', 'Latest task') }}: {{ status.task.status }} / {{ status.task.stage }}</p>
      <p v-if="safeTaskError" class="text-red-600">{{ safeTaskError }}</p>
      <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      <button v-if="encryption && !encryption.configured" type="button" class="btn btn-secondary" :disabled="busy" @click="initialize">{{ text('初始化凭据加密', 'Initialize credential encryption') }}</button>
      <fieldset :disabled="busy || !loaded || !encryption?.configured || running" class="space-y-3 disabled:opacity-60">
        <label class="block">{{ text('登录邮箱', 'Login email') }}<input v-model.trim="form.login_email" class="input mt-1" type="email" autocomplete="off" data-testid="excel-login-email" /></label>
        <label class="block">{{ text('登录密码', 'Password') }} · {{ configuredText(status.config?.password_configured) }}<input v-model="password" class="input mt-1" type="password" autocomplete="new-password" data-testid="excel-password" :placeholder="text('留空保留已保存的密码', 'Leave blank to keep the saved password')" :disabled="clearPassword" /></label>
        <label class="flex gap-2"><input v-model="clearPassword" type="checkbox" />{{ text('清除已保存的密码', 'Clear saved password') }}</label>
        <label class="block">TOTP · {{ configuredText(status.config?.totp_configured) }}<input v-model="totp" class="input mt-1" type="password" autocomplete="off" data-testid="excel-totp" :placeholder="text('留空保留已保存的 TOTP 密钥', 'Leave blank to keep the saved TOTP secret')" :disabled="clearTotp" /></label>
        <label class="flex gap-2"><input v-model="clearTotp" type="checkbox" />{{ text('清除已保存的 TOTP 密钥', 'Clear saved TOTP secret') }}</label>
        <label class="block">{{ text('授权代理', 'Authorization proxy') }}
          <select v-model="form.proxy_source" class="input mt-1">
            <option value="account">{{ text('跟随账号代理', 'Use account proxy') }}</option>
            <option value="managed_proxy">{{ text('指定托管代理', 'Select managed proxy') }}</option>
            <option value="mihomo">Mihomo</option>
          </select>
        </label>
        <label v-if="form.proxy_source === 'managed_proxy'" class="block">{{ text('托管代理 ID', 'Managed proxy ID') }}<input v-model.number="form.proxy_id" class="input mt-1" type="number" min="1" step="1" /></label>
        <button type="button" class="btn btn-secondary" data-testid="excel-save" @click="save">{{ text('保存授权材料', 'Save login credentials') }}</button>
      </fieldset>
      <div class="flex gap-2">
        <button type="button" class="btn btn-primary" :disabled="busy || running || !status.config?.configured || !encryption?.configured" data-testid="excel-authorize" @click="authorize">{{ text('开始 BPS 授权', 'Authorize BPS') }}</button>
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="load">{{ text('刷新状态', 'Refresh status') }}</button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import * as api from '@/api/admin/excelAuthorization'
import ExcelBPSCredentialStatus from './ExcelBPSCredentialStatus.vue'
const props = withDefaults(defineProps<{ account: Account; active?: boolean }>(), { active: true })
const emit = defineEmits<{ updated: [] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const configuredText = (configured?: boolean) => configured ? text('已配置', 'Configured') : text('未配置', 'Not configured')
const expanded = ref(false)
const loaded = ref(false)
const busy = ref(false)
const error = ref('')
const password = ref('')
const totp = ref('')
const clearPassword = ref(false)
const clearTotp = ref(false)
const status = ref<api.ExcelAuthorizationStatus>({})
const encryption = ref<api.CredentialEncryptionStatus>()
const runtime = ref<api.ReauthRuntimeStatus>()
const form = reactive({ login_email: '', proxy_source: 'account' as api.ExcelAuthorizationConfig['proxy_source'], proxy_id: undefined as number | undefined })
const running = computed(() => ['queued', 'running', 'pending', 'claimed'].includes(status.value.task?.status || ''))
const safeTaskError = computed(() => /^[a-zA-Z0-9_.:-]{1,160}$/.test(status.value.task?.error || '') ? status.value.task?.error : status.value.task?.error ? text('授权失败，请检查授权材料与服务状态。', 'Authorization failed. Check the login credentials and worker status.') : '')
let generation = 0
let timer: ReturnType<typeof setTimeout> | undefined
const clearSecrets = () => { password.value = ''; totp.value = ''; clearPassword.value = false; clearTotp.value = false }
const stopPoll = () => { if (timer) clearTimeout(timer); timer = undefined }
const valid = (run: number, id: number) => run === generation && props.active && expanded.value && props.account.id === id
function applyStatus(next: api.ExcelAuthorizationStatus, syncForm: boolean) {
  const previous = status.value.task
  status.value = next
  if (syncForm) {
    form.login_email = next.config?.login_email || String(props.account.credentials?.email || '')
    form.proxy_source = next.config?.proxy_source || 'account'
    form.proxy_id = next.config?.proxy_id
  }
  if (previous && ['queued', 'running', 'pending', 'claimed'].includes(previous.status) && next.task && !running.value) emit('updated')
}
function schedulePoll(run: number, id: number) {
  stopPoll()
  if (!running.value || !valid(run, id)) return
  timer = setTimeout(async () => {
    try {
      const next = await api.getExcelAuthorization(id)
      if (!valid(run, id)) return
      applyStatus(next, false)
      schedulePoll(run, id)
    } catch {
      if (valid(run, id)) error.value = text('状态刷新失败，请手动重试。', 'Could not refresh authorization status. Please retry.')
    }
  }, 3000)
}
async function load() {
  stopPoll()
  const run = ++generation, id = props.account.id
  busy.value = true
  error.value = ''
  try {
    const [next, enc, worker] = await Promise.all([api.getExcelAuthorization(id), api.getCredentialEncryption(), api.getReauthRuntime()])
    if (!valid(run, id)) return
    applyStatus(next, true)
    encryption.value = enc
    runtime.value = worker
    loaded.value = true
    schedulePoll(run, id)
  } catch {
    if (valid(run, id)) error.value = text('无法读取授权状态，请重试。', 'Could not load authorization status. Please retry.')
  } finally { if (valid(run, id)) busy.value = false }
}
async function perform(operation: (id: number) => Promise<void>) {
  if (busy.value) return
  stopPoll()
  const run = ++generation, id = props.account.id
  busy.value = true
  error.value = ''
  try {
    await operation(id)
    if (!valid(run, id)) return
    clearSecrets()
    await load()
    if (props.account.id === id && expanded.value) emit('updated')
  } catch {
    if (valid(run, id)) error.value = text('操作失败，请检查配置后重试。', 'The operation failed. Check the configuration and retry.')
  } finally { if (valid(run, id)) busy.value = false }
}
const initialize = () => perform(async () => { await api.initializeCredentialEncryption() })
const authorize = () => perform(async id => { await api.startExcelAuthorization(id) })
function save() {
  if (!form.login_email.trim() || (form.proxy_source === 'managed_proxy' && (!Number.isInteger(form.proxy_id) || Number(form.proxy_id) <= 0))) {
    error.value = text('请填写登录邮箱和有效的代理 ID。', 'Enter a login email and a valid proxy ID.')
    return
  }
  const input: api.ExcelAuthorizationInput = { ...form, credential_mode: 'password_totp', engine: 'local_worker', proxy_id: form.proxy_source === 'managed_proxy' ? form.proxy_id : undefined, password: clearPassword.value ? undefined : password.value || undefined, totp_secret: clearTotp.value ? undefined : totp.value.trim() || undefined, clear_password: clearPassword.value, clear_totp: clearTotp.value }
  void perform(async id => { await api.saveExcelAuthorization(id, input) })
}
function reset() {
  generation++
  stopPoll()
  clearSecrets()
  expanded.value = false
  busy.value = false
  loaded.value = false
  status.value = {}
  encryption.value = undefined
  runtime.value = undefined
  error.value = ''
}
function toggle() {
  if (expanded.value) reset()
  else { expanded.value = true; void load() }
}
watch(() => [props.account.id, props.active], reset)
onBeforeUnmount(reset)
</script>
