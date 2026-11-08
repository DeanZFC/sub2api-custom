<template>
  <div class="space-y-4">
    <label class="block space-y-1">{{ t('qqbot.simple.aiSource') }}
      <select :value="model.ai_connection || 'external'" class="input" data-testid="ai-source" @change="changeSource"><option value="site">{{ t('qqbot.simple.siteAI') }}</option><option value="external">{{ t('qqbot.simple.externalAI') }}</option></select>
    </label>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <template v-if="model.ai_connection === 'site'">
      <p class="rounded-lg bg-primary-50 p-3 text-sm text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('qqbot.simple.managedHelp') }}</p>
      <button v-if="!options" type="button" class="btn btn-secondary" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
      <label class="block space-y-1">{{ t('qqbot.simple.siteGroup') }}<select v-model.number="model.site_group_id" required class="input" data-testid="site-group"><option :value="0" disabled>{{ t('qqbot.simple.chooseGroup') }}</option><option v-for="group in options?.groups || []" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
      <p v-if="options && !options.groups.length" class="text-sm text-amber-700">{{ t('qqbot.simple.noSiteGroups') }}</p>
      <label v-if="model.site_api_key_id" class="flex items-start gap-2 text-sm"><input v-model="regenerate" type="checkbox" class="mt-1" />{{ t('qqbot.simple.regenerateKey') }}</label>
      <label class="block space-y-1">{{ t('qqbot.simple.keyQuota') }}<input v-model.number="quota" type="number" class="input" min="0" max="1000000" step="0.01" required :disabled="existingKey && !regenerate" data-testid="site-quota" /></label>
      <p class="text-xs text-gray-500">{{ t('qqbot.simple.quotaHelp') }}</p>
      <p v-if="existingKey" class="text-sm text-gray-500">{{ t('qqbot.simple.keyUsage', { used: options?.current_used ?? 0, quota: options?.current_quota ?? 0, id: model.site_api_key_id }) }} <RouterLink class="text-primary-600 underline" to="/keys">{{ t('qqbot.simple.manageKey') }}</RouterLink></p>
    </template>
    <div v-else class="grid gap-4 md:grid-cols-2">
      <label class="space-y-1">Base URL<input v-model.trim="model.base_url" class="input" placeholder="https://api.example.com/v1" /></label>
      <label class="space-y-1">API Key<input v-model="model.api_key" type="password" autocomplete="new-password" class="input" :placeholder="t(model.api_key_set ? 'qqbot.secretStored' : 'qqbot.secretEmpty')" /><span class="flex gap-2 text-xs"><input v-model="model.clear_api_key" type="checkbox" />{{ t('qqbot.clearSecret') }}</span></label>
    </div>
    <label class="block space-y-1">{{ t('qqbot.chatModel') }}<input v-model.trim="model.chat_model" :list="listId" class="input" maxlength="200" :required="!model.chat_disabled" data-testid="setup-chat-model" /><datalist :id="listId"><option v-for="name in models" :key="name" :value="name" /></datalist></label>
    <p v-if="model.ai_connection === 'site'" class="text-xs text-gray-500">{{ t('qqbot.simple.candidates') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQBotConfig, type QQSetupOptions } from '@/api/qqbot'
const model = defineModel<QQBotConfig>({ required: true })
const quota = defineModel<number>('quota', { required: true })
const regenerate = defineModel<boolean>('regenerate', { default: false })
const { t } = useI18n()
const options = ref<QQSetupOptions>(), error = ref(''), loading = ref(false)
const originalGroup = ref(model.value.site_api_key_id ? model.value.site_group_id : 0)
const existingKey = computed(() => !!model.value.site_api_key_id && originalGroup.value === model.value.site_group_id)
const models = computed(() => options.value?.groups.find(group => group.id === model.value.site_group_id)?.models || [])
const listId = useId()
let alive = true
async function load() { loading.value = true; error.value = ''; try { const result = await qqbotAPI.setupOptions(); if (alive) { options.value = result; if (existingKey.value) quota.value = result.current_quota } } catch (e) { if (alive) error.value = (e as Error).message } finally { if (alive) loading.value = false } }
function changeSource(event: Event) { model.value.ai_connection = (event.target as HTMLSelectElement).value as 'site' | 'external'; model.value.api_key = ''; model.value.clear_api_key = false; if (model.value.ai_connection === 'external') { model.value.base_url = ''; model.value.api_key_set = false } }
watch(() => model.value.site_api_key_id, (current, previous) => { if (current && current !== previous) { originalGroup.value = model.value.site_group_id; void load() } })
onMounted(load)
onBeforeUnmount(() => { alive = false })
</script>
