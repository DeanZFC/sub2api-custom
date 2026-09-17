<template>
  <section class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="prism-settings">
    <div class="flex items-center justify-between gap-4">
      <div>
        <label id="prism-enabled-label" class="input-label mb-0">{{ t('admin.accounts.openai.prism.title') }}</label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openai.prism.description') }}</p>
      </div>
      <button
        type="button"
        role="switch"
        aria-labelledby="prism-enabled-label"
        :aria-checked="enabled"
        data-testid="prism-enabled"
        :class="[
          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
          enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
        ]"
        @click="emit('update:enabled', !enabled)"
      >
        <span :class="['pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow transition-transform', enabled ? 'translate-x-5' : 'translate-x-0']" />
      </button>
    </div>
    <template v-if="enabled">
      <div class="space-y-2 rounded-lg bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
        <p>{{ t('admin.accounts.openai.prism.contextHint') }}</p>
        <p>{{ t('admin.accounts.openai.prism.unsupportedHint') }}</p>
        <p>{{ t('admin.accounts.openai.prism.usageHint') }}</p>
      </div>
      <div>
        <label for="prism-auth-mode" class="input-label">{{ t('admin.accounts.openai.prism.authMode') }}</label>
        <select
          id="prism-auth-mode"
          :value="authMode"
          class="input"
          data-testid="prism-auth-mode"
          @change="emit('update:authMode', ($event.target as HTMLSelectElement).value as 'account' | 'cookie')"
        >
          <option value="account">{{ t('admin.accounts.openai.prism.authModeAccount') }}</option>
          <option value="cookie">{{ t('admin.accounts.openai.prism.authModeCookie') }}</option>
        </select>
        <p v-if="authMode === 'account'" class="input-hint">{{ t('admin.accounts.openai.prism.authModeAccountHint') }}</p>
      </div>
      <div v-if="authMode === 'cookie'">
        <label for="prism-cookie" class="input-label">{{ t('admin.accounts.openai.prism.cookie') }}</label>
        <input
          id="prism-cookie"
          :value="cookie"
          type="password"
          autocomplete="new-password"
          spellcheck="false"
          class="input"
          data-testid="prism-cookie"
          :placeholder="t(cookieConfigured ? 'admin.accounts.openai.prism.cookieKeepPlaceholder' : 'admin.accounts.openai.prism.cookiePlaceholder')"
          @input="emit('update:cookie', ($event.target as HTMLInputElement).value)"
        />
        <p class="input-hint">{{ t(cookieConfigured ? 'admin.accounts.openai.prism.cookieConfiguredHint' : 'admin.accounts.openai.prism.cookieHint') }}</p>
      </div>
      <div>
        <label for="prism-timeout" class="input-label">{{ t('admin.accounts.openai.prism.timeout') }}</label>
        <input
          id="prism-timeout"
          :value="timeoutSeconds"
          type="number"
          min="30"
          max="600"
          step="1"
          required
          class="input"
          data-testid="prism-timeout"
          @input="emit('update:timeoutSeconds', Number(($event.target as HTMLInputElement).value))"
        />
        <p class="input-hint">{{ t('admin.accounts.openai.prism.timeoutHint') }}</p>
      </div>
      <details>
        <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.accounts.openai.prism.advanced') }}</summary>
        <div class="mt-3">
          <label for="prism-action-id" class="input-label">{{ t('admin.accounts.openai.prism.actionId') }}</label>
          <input
            id="prism-action-id"
            :value="conversationActionId"
            type="text"
            autocomplete="off"
            spellcheck="false"
            pattern="[a-fA-F0-9]{42}"
            class="input font-mono"
            data-testid="prism-action-id"
            :placeholder="t('admin.accounts.openai.prism.actionIdPlaceholder')"
            @input="emit('update:conversationActionId', ($event.target as HTMLInputElement).value)"
          />
          <p class="input-hint">{{ t('admin.accounts.openai.prism.actionIdHint') }}</p>
        </div>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{
  enabled: boolean
  authMode: 'account' | 'cookie'
  cookie: string
  cookieConfigured: boolean
  timeoutSeconds: number
  conversationActionId: string
}>()

const emit = defineEmits<{
  'update:enabled': [value: boolean]
  'update:authMode': [value: 'account' | 'cookie']
  'update:cookie': [value: string]
  'update:timeoutSeconds': [value: number]
  'update:conversationActionId': [value: string]
}>()
const { t } = useI18n()
</script>
