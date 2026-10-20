<template>
  <section class="card" @keydown.enter="submitOnEnter">
    <div class="border-b border-gray-100 p-6 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('gptDirect.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('gptDirect.admin.intro') }}</p>
    </div>
    <div class="space-y-5 p-6">
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <button v-if="loadFailed" type="button" class="btn btn-secondary" @click="load">{{ t('gptDirect.retry') }}</button>
      <fieldset :disabled="loading || saving || loadFailed" class="space-y-5 disabled:opacity-60">
        <label class="flex items-center justify-between gap-4">
          <span class="font-medium">{{ t('gptDirect.admin.enabled') }}</span>
          <Toggle v-model="form.enabled" :disabled="loading || saving || loadFailed" />
        </label>
        <div class="grid gap-5 md:grid-cols-2">
          <label class="block">
            <span class="input-label">{{ t('gptDirect.admin.baseURL') }}</span>
            <input v-model.trim="form.base_url" type="url" class="input" placeholder="https://recharge.example.com" autocomplete="off" />
            <span class="mt-1 block text-xs text-gray-500">{{ t('gptDirect.admin.baseHint') }}</span>
          </label>
          <label class="block">
            <span class="input-label">{{ t('gptDirect.admin.tenant') }}</span>
            <input v-model.trim="form.tenant_slug" class="input" placeholder="ctai" maxlength="32" autocomplete="off" />
            <span class="mt-1 block text-xs text-gray-500">{{ t('gptDirect.admin.tenantHint') }}</span>
          </label>
          <label class="block">
            <span class="input-label">{{ t('gptDirect.admin.purchaseURL') }}</span>
            <input v-model.trim="form.purchase_url" type="url" class="input" placeholder="https://shop.example.com" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('gptDirect.admin.buttonText') }}</span>
            <input v-model.trim="form.button_text" class="input" :placeholder="t('gptDirect.buy')" maxlength="40" />
          </label>
        </div>
        <label class="block">
          <span class="input-label">{{ t('gptDirect.admin.description') }}</span>
          <textarea v-model="form.description" class="input" rows="3" maxlength="2000" :placeholder="t('gptDirect.purchaseHint')" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('gptDirect.admin.supportContent') }}</span>
          <textarea v-model="form.support_url" class="input" rows="3" maxlength="2048" :placeholder="t('gptDirect.admin.supportPlaceholder')" />
          <span class="mt-1 block text-xs text-gray-500">{{ t('gptDirect.admin.supportHint') }}</span>
        </label>
        <p class="rounded-lg bg-gray-50 p-3 text-sm text-gray-500 dark:bg-dark-800">{{ t('gptDirect.admin.proxyHint') }}</p>
        <div class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <label class="flex items-center justify-between gap-4">
            <span class="font-medium">{{ t('gptDirect.admin.lotteryEnabled') }}</span>
            <Toggle v-model="form.lottery_reward_enabled" :disabled="loading || saving || loadFailed" />
          </label>
          <label v-if="form.lottery_reward_enabled" class="block">
            <span class="input-label">{{ t('gptDirect.admin.lotteryChances') }}</span>
            <input v-model.number="form.lottery_chances" type="number" min="1" max="1000" step="1" class="input max-w-xs" />
          </label>
          <p class="text-xs leading-5 text-gray-500">{{ t('gptDirect.admin.lotteryHint') }}</p>
        </div>
        <div class="flex items-center justify-end gap-3">
          <span v-if="saved" role="status" class="text-sm text-emerald-600">{{ t('gptDirect.admin.saved') }}</span>
          <button type="button" class="btn btn-primary" :disabled="loading || saving || loadFailed" @click="save">
            {{ saving ? t('gptDirect.working') : t('gptDirect.admin.save') }}
          </button>
        </div>
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { gptDirectAPI, type GPTDirectSettings } from '@/api/gptDirect'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const app = useAppStore()
const loading = ref(true)
const saving = ref(false)
const loadFailed = ref(false)
const saved = ref(false)
const error = ref('')
const form = reactive<GPTDirectSettings>({ enabled: false, base_url: '', tenant_slug: 'ctai', purchase_url: '', button_text: '', description: '', support_url: '', lottery_reward_enabled: false, lottery_chances: 1 })

async function load() {
  loading.value = true
  loadFailed.value = false
  error.value = ''
  try { Object.assign(form, await gptDirectAPI.settings()) }
  catch { loadFailed.value = true; error.value = t('gptDirect.loadFailed') }
  finally { loading.value = false }
}
async function save() {
  if (saving.value || loading.value || loadFailed.value) return
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    Object.assign(form, await gptDirectAPI.saveSettings({ ...form }))
    saved.value = true
    await app.fetchPublicSettings(true)
  } catch (failure) {
    error.value = (failure as { reason?: string })?.reason === 'REWARD_UNAVAILABLE'
      ? t('gptDirect.admin.rewardKeyRequired') : t('gptDirect.admin.saveFailed')
  }
  finally { saving.value = false }
}
function submitOnEnter(event: KeyboardEvent) {
  // This panel lives inside the general settings form. Enter in our inputs
  // must save this panel, without submitting that unrelated form.
  if (event.target instanceof HTMLInputElement) {
    event.preventDefault()
    void save()
  }
}
onMounted(load)
</script>
