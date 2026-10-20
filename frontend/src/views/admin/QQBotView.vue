<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-2xl font-semibold">{{ t('qqbot.basicConfig') }}</h1><p class="mt-1 text-sm text-gray-500">{{ t('qqbot.description') }}</p></div>
        <button class="btn btn-secondary" :disabled="busy || refreshingRuntime" @click="refreshRuntime">{{ t('common.refresh') }}</button>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-lg bg-green-50 p-4 text-green-700 dark:bg-green-950">{{ notice }}</p>
      <p v-if="!form" class="text-gray-500">{{ t('common.loading') }}</p>
      <form v-else class="space-y-6" @submit.prevent="save">
        <section class="card space-y-4 p-6">
          <div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('qqbot.connection') }}</h2><label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />{{ t('common.enabled') }}</label></div>
          <p v-if="runtime && !runtime.encryption_ready" class="text-sm text-amber-700">{{ t('qqbot.encryptionRequired') }}</p>
          <div class="grid gap-4 md:grid-cols-2">
            <label class="space-y-1">AppID<input v-model.trim="form.app_id" class="input" autocomplete="off" /></label>
            <label class="space-y-1">AppSecret<input v-model="form.app_secret" type="password" class="input" autocomplete="new-password" :placeholder="secretHint(form.app_secret_set)" /><span class="flex gap-2 text-xs"><input v-model="form.clear_app_secret" type="checkbox" />{{ t('qqbot.clearSecret') }}</span></label>
          </div>
          <label class="flex items-center gap-2"><input v-model="form.sandbox" type="checkbox" />{{ t('qqbot.sandbox') }}</label>
          <div class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800"><span>{{ t('qqbot.webhook') }}</span><code class="mt-1 block break-all select-all">{{ webhookURL }}</code><p class="mt-2 text-gray-500">{{ t('qqbot.eventsHelp') }}</p></div>
          <label class="block space-y-1">{{ t('qqbot.allowedGroups') }}<textarea v-model="groupsText" rows="3" class="input font-mono" :placeholder="t('qqbot.groupsPlaceholder')" /></label>
          <p class="text-sm text-gray-500">{{ t('qqbot.groupsHelp') }}</p>
          <div v-if="runtime?.groups.length" class="space-y-2">
            <p class="text-sm font-medium">{{ t('qqbot.discoveredGroups') }}</p>
            <p class="text-xs text-gray-500">{{ t('qqbot.discoveredGroupsHelp') }}</p>
            <div v-for="group in runtime.groups" :key="group.id" class="flex flex-wrap items-center justify-between gap-2 rounded-lg border p-2 dark:border-dark-700">
              <code class="break-all text-sm">{{ group.id }}</code>
              <div class="flex shrink-0 gap-2">
                <button type="button" class="btn btn-secondary text-xs" @click="addGroup(group.id)">{{ t('common.add') }}</button>
                <button type="button" class="btn btn-secondary text-xs" :disabled="busy || refreshingRuntime" :aria-label="t('qqbot.deleteDiscoveredGroup', { id: group.id })" @click="deleteDiscoveredGroup(group.id)">{{ t('common.delete') }}</button>
              </div>
            </div>
          </div>
        </section>
        <section class="card space-y-4 p-6" :aria-label="t('qqbot.priceNotifications.title')">
          <h2 class="text-lg font-semibold">{{ t('qqbot.priceNotifications.title') }}</h2>
          <p class="text-sm text-gray-500">{{ t('qqbot.priceNotifications.help') }}</p>
          <label class="flex items-center gap-2"><input v-model="priceNotifications.enabled" data-testid="price-notifications-enabled" type="checkbox" />{{ t('qqbot.priceNotifications.enabled') }}</label>
          <label class="flex items-center gap-2"><input v-model="priceNotifications.scheduled" data-testid="price-notifications-scheduled" type="checkbox" />{{ t('qqbot.priceNotifications.scheduled') }}</label>
          <label class="block space-y-1">{{ t('qqbot.priceNotifications.groups') }}<textarea v-model="notificationGroupsText" data-testid="price-notification-groups" rows="2" class="input font-mono" :placeholder="t('qqbot.priceNotifications.groupsPlaceholder')" /></label>
          <p class="text-xs text-gray-500">{{ t('qqbot.priceNotifications.permission') }}</p>
          <details>
            <summary class="cursor-pointer text-sm font-medium">{{ t('qqbot.priceNotifications.records') }}</summary>
            <p v-if="!notificationJobs.length" class="mt-3 text-sm text-gray-500">{{ t('qqbot.priceNotifications.empty') }}</p>
            <div v-for="job in notificationJobs" :key="job.id" class="mt-3 space-y-1 rounded-lg border p-3 text-sm dark:border-dark-700">
              <p class="break-all">{{ job.group_id }}</p>
              <p>{{ new Date(job.created_at).toLocaleString() }} · {{ t(`qqbot.priceNotifications.status.${job.status}`, job.status) }}</p>
              <p v-if="job.error" class="text-red-600 dark:text-red-400">{{ job.error }}</p>
            </div>
          </details>
        </section>
        <section class="card space-y-4 p-6" :aria-label="t('qqbot.customCommands')">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <h2 class="text-lg font-semibold">{{ t('qqbot.customCommands') }}</h2>
            <button type="button" class="btn btn-secondary" :disabled="busy || form.custom_commands.length >= 20" @click="addCustomCommand">{{ t('qqbot.addCustomCommand') }}</button>
          </div>
          <p class="text-sm text-gray-500">{{ t('qqbot.customCommandsHelp') }}</p>
          <p v-if="!form.custom_commands.length" class="text-sm text-gray-500">{{ t('qqbot.noCustomCommands') }}</p>
          <fieldset v-for="(custom, index) in form.custom_commands" :key="index" :disabled="busy" class="space-y-3 rounded-lg border p-4 dark:border-dark-700">
            <legend class="px-1 text-sm font-medium">{{ t('qqbot.customCommandEntry', { index: index + 1 }) }}</legend>
            <div class="flex flex-wrap items-center justify-between gap-3">
              <label class="flex items-center gap-2"><input v-model="custom.enabled" type="checkbox" />{{ t('common.enabled') }}</label>
              <button type="button" class="btn btn-secondary text-sm" @click="form.custom_commands.splice(index, 1)">{{ t('common.delete') }}</button>
            </div>
            <label class="block space-y-1">{{ t('qqbot.customCommandName') }}<input v-model.trim="custom.command" class="input font-mono" placeholder="/a" required maxlength="33" /></label>
            <label class="block space-y-1">{{ t('qqbot.customCommandReply') }}<textarea v-model="custom.reply" rows="4" class="input" :placeholder="t('qqbot.customReplyPlaceholder')" required /></label>
          </fieldset>
        </section>
        <section class="card space-y-4 p-6">
          <h2 class="text-lg font-semibold">{{ t('qqbot.ai') }}</h2>
          <div class="grid gap-4 md:grid-cols-2">
            <label class="space-y-1">Base URL<input v-model.trim="form.base_url" class="input" placeholder="https://api.example.com/v1" /><span class="text-xs text-gray-500">{{ t('qqbot.baseHelp') }}</span></label>
            <label class="space-y-1">API Key<input v-model="form.api_key" type="password" class="input" autocomplete="new-password" :placeholder="secretHint(form.api_key_set)" /><span class="flex gap-2 text-xs"><input v-model="form.clear_api_key" type="checkbox" />{{ t('qqbot.clearSecret') }}</span></label>
            <label class="space-y-1">{{ t('qqbot.chatModel') }}<input v-model.trim="form.chat_model" class="input" /></label>
            <label class="space-y-1">{{ t('qqbot.protocol') }}<select v-model="form.chat_protocol" class="input"><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label>
          </div>
          <label class="block space-y-1">{{ t('qqbot.systemPrompt') }}<textarea v-model="form.system_prompt" rows="3" class="input" /></label>
          <div class="grid gap-4 md:grid-cols-2">
            <label class="space-y-1">{{ t('qqbot.maxTokens') }}<input v-model.number="form.max_output_tokens" class="input" type="number" min="64" max="8192" /></label>
            <label class="space-y-1">{{ t('qqbot.history') }}<input v-model.number="form.history_messages" data-testid="history-messages" aria-describedby="qq-history-help" class="input" type="number" min="0" max="100" step="1" required /></label>
          </div>
          <p id="qq-history-help" class="text-xs text-gray-500">{{ t('qqbot.historyHelp') }}</p>
        </section>
        <section class="card space-y-4 p-6">
          <div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('qqbot.images') }}</h2><label class="flex items-center gap-2"><input v-model="form.image_enabled" type="checkbox" />{{ t('common.enabled') }}</label></div>
          <div class="grid gap-4 md:grid-cols-2">
            <label class="space-y-1">{{ t('qqbot.imageModel') }}<input v-model.trim="form.image_model" class="input" /></label>
            <label class="space-y-1">{{ t('qqbot.imageSize') }}<select v-model="form.image_size" class="input"><option>1024x1024</option><option>1536x1024</option><option>1024x1536</option><option>auto</option></select></label>
            <label class="space-y-1">{{ t('qqbot.imageBase') }}<input v-model.trim="form.image_base_url" class="input" :placeholder="t('qqbot.reuseChat')" /></label>
            <label class="space-y-1">{{ t('qqbot.imageKey') }}<input v-model="form.image_api_key" type="password" class="input" autocomplete="new-password" :placeholder="secretHint(form.image_api_key_set)" /><span class="flex gap-2 text-xs"><input v-model="form.clear_image_api_key" type="checkbox" />{{ t('qqbot.clearSecret') }}</span></label>
          </div>
          <p class="text-sm text-gray-500">{{ t('qqbot.imageHelp') }}</p>
        </section>
        <section class="card space-y-4 p-6">
          <h2 class="text-lg font-semibold">{{ t('qqbot.limits') }}</h2><p class="text-sm text-gray-500">{{ t('qqbot.limitsHelp') }}</p>
          <div class="grid gap-4 md:grid-cols-2">
            <label v-for="field in limitFields" :key="field.key" class="space-y-1">{{ t(`qqbot.${field.label}`) }}<input v-model.number="form[field.key]" type="number" min="0" :max="field.max" class="input" /></label>
          </div>
        </section>
        <div class="sticky bottom-3 z-10 flex flex-wrap items-center gap-3 rounded-xl border bg-white p-4 shadow-lg dark:border-dark-700 dark:bg-dark-900">
          <button type="submit" :disabled="busy" class="btn btn-primary">{{ t('common.save') }}</button>
          <button v-for="kind in probeKinds" :key="kind" type="button" class="btn btn-secondary" :disabled="busy || dirty" @click="probe(kind)">{{ t(`qqbot.test_${kind}`) }}</button>
          <span class="text-xs text-gray-500">{{ dirty ? t('qqbot.saveBeforeTest') : t('qqbot.testCost') }}</span>
        </div>
      </form>

    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { qqbotAPI, type QQBotConfig, type QQBotRuntime } from '@/api/qqbot'
const { t } = useI18n()
const form = ref<QQBotConfig | null>(null)
const runtime = ref<QQBotRuntime | null>(null)
const groupsText = ref('')
const notificationGroupsText = ref('')
const priceNotifications = ref({ enabled: false, scheduled: true, groups: [] as string[] })
const notificationJobs = computed(() => runtime.value?.jobs.filter(job => job.kind === 'price_notification') ?? [])
const savedSnapshot = ref('')
const busy = ref(false)
const refreshingRuntime = ref(false)
const error = ref('')
const notice = ref('')
const probeKinds = ['qq', 'chat', 'image'] as const
const limitFields = [
  { key: 'daily_chat_limit', label: 'dailyChat', max: 100000 },
  { key: 'daily_image_limit', label: 'dailyImage', max: 100000 },
  { key: 'total_daily_chat_limit', label: 'totalChat', max: 100000 },
  { key: 'total_daily_image_limit', label: 'totalImage', max: 100000 },
  { key: 'cooldown_seconds', label: 'cooldown', max: 3600 }
] as const
const snapshot = computed(() => JSON.stringify([form.value, groupsText.value, priceNotifications.value, notificationGroupsText.value]))
const dirty = computed(() => snapshot.value !== savedSnapshot.value)
const webhookURL = computed(() => `${window.location.origin}/api/v1/qqbot/webhook`)
function secretHint(set: boolean) { return t(set ? 'qqbot.secretStored' : 'qqbot.secretEmpty') }
function message(e: unknown) { return (e as {message?: string})?.message || t('common.error') }
function apply(c: QQBotConfig) {
  form.value = { ...c, custom_commands: c.custom_commands ?? [] }
  groupsText.value = (c.allowed_groups || []).join('\n')
  priceNotifications.value = { enabled: false, scheduled: true, ...c.price_notifications, groups: c.price_notifications?.groups ?? [] }
  notificationGroupsText.value = priceNotifications.value.groups.join('\n')
  savedSnapshot.value = snapshot.value
}
function addCustomCommand() {
  if (!form.value || busy.value || form.value.custom_commands.length >= 20) return
  form.value.custom_commands.push({ command: '', reply: '', enabled: true })
}
async function refreshRuntime() {
  if (refreshingRuntime.value) return
  refreshingRuntime.value = true
  try { runtime.value = await qqbotAPI.runtime() } catch (e) { error.value = message(e) } finally { refreshingRuntime.value = false }
}
async function deleteDiscoveredGroup(id: string) {
  if (busy.value || refreshingRuntime.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await qqbotAPI.deleteDiscoveredGroup(id)
    if (runtime.value) runtime.value.groups = runtime.value.groups.filter(group => group.id !== id)
    notice.value = t('qqbot.discoveredGroupDeleted')
  } catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function save() {
  if (!form.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { apply(await qqbotAPI.save({ ...form.value, price_notifications: { ...priceNotifications.value, groups: [...new Set(notificationGroupsText.value.split(/[\s,，]+/).filter(Boolean))] }, allowed_groups: [...new Set(groupsText.value.split(/[\s,，]+/).filter(Boolean))] })); notice.value = t('common.saved'); await refreshRuntime() }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
function addGroup(id: string) { groupsText.value = [...new Set([...groupsText.value.split(/[\s,，]+/).filter(Boolean), id])].join('\n') }
async function probe(kind: typeof probeKinds[number]) {
  busy.value = true; error.value = ''; notice.value = ''
  try { notice.value = (await qqbotAPI.probe(kind)).message } catch (e) { error.value = message(e) } finally { busy.value = false }
}
onMounted(async () => { busy.value = true; try { apply(await qqbotAPI.config()); await refreshRuntime() } catch (e) { error.value = message(e) } finally { busy.value = false } })
</script>
