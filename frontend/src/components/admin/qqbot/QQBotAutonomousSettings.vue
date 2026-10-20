<template>
  <section class="card space-y-4 p-6" :aria-label="t('qqbot.autonomous.title')">
    <h2 class="text-lg font-semibold">{{ t('qqbot.autonomous.title') }}</h2>
    <p class="text-sm text-gray-500">{{ t('qqbot.autonomous.help') }}</p>
    <label class="flex items-center gap-2"><input v-model="value.enabled" data-testid="autonomous-enabled" type="checkbox" />{{ t('qqbot.autonomous.enabled') }}</label>
    <div class="grid gap-4 md:grid-cols-2">
      <label class="space-y-1">{{ t('qqbot.autonomous.groups') }}<textarea v-model="groups" data-testid="autonomous-groups" class="input font-mono" rows="2" :required="value.enabled" /></label>
      <label class="space-y-1">{{ t('qqbot.autonomous.names') }}<textarea v-model="names" data-testid="autonomous-names" class="input" rows="2" /></label>
      <label class="space-y-1 md:col-span-2">{{ t('qqbot.autonomous.topics') }}<textarea v-model="topics" data-testid="autonomous-topics" class="input" rows="2" /></label>
      <label v-for="field in fields" :key="field.key" class="space-y-1">{{ t('qqbot.autonomous.' + field.key) }}<input v-model.number="value[field.key]" :data-testid="'autonomous-' + field.key" type="number" :min="field.min" :max="field.max" step="1" required class="input" /></label>
    </div>
    <label class="flex items-center gap-2"><input v-model="value.mention_user" data-testid="autonomous-mention" type="checkbox" />{{ t('qqbot.autonomous.mention') }}</label>
    <p class="text-xs text-gray-500">{{ t('qqbot.autonomous.limitsHelp') }}</p>
    <details>
      <summary class="cursor-pointer text-sm font-medium">{{ t('qqbot.autonomous.stats') }}</summary>
      <p v-if="!runtime?.autonomous_stats?.length" class="mt-3 text-sm text-gray-500">{{ t('qqbot.autonomous.empty') }}</p>
      <div v-for="stat in runtime?.autonomous_stats ?? []" :key="stat.group_id + stat.reason" class="mt-2 text-sm break-all">{{ stat.group_id }} · {{ reasonLabel(stat.reason) }}: {{ stat.count }}</div>
    </details>
    <details>
      <summary class="cursor-pointer text-sm font-medium">{{ t('qqbot.autonomous.records') }}</summary>
      <p v-if="!jobs.length" class="mt-3 text-sm text-gray-500">{{ t('qqbot.autonomous.empty') }}</p>
      <div v-for="job in jobs" :key="job.id" class="mt-3 space-y-1 rounded-lg border p-3 text-sm dark:border-dark-700">
        <p class="break-all">{{ job.group_id }} · {{ new Date(job.created_at).toLocaleString() }}</p>
        <p>{{ reasonLabel(job.trigger_reason || '') }} · {{ reasonLabel(job.decision_reason || job.status) }}</p>
        <p>Tokens: {{ job.input_tokens }} / {{ job.output_tokens }}</p>
        <p v-if="job.error" class="text-red-600 dark:text-red-400">{{ job.error }}</p>
      </div>
    </details>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { QQAutonomousConfig, QQBotRuntime } from '@/api/qqbot'
const value = defineModel<QQAutonomousConfig>({ required: true })
const props = defineProps<{ runtime: QQBotRuntime | null }>()
const { t, te } = useI18n()
const list = (key: 'groups' | 'names' | 'topics') => computed({
  get: () => (value.value[key] ?? []).join('\n'),
  set: (text: string) => { value.value[key] = [...new Set(text.split(/[\n,，]+/).map(s => s.trim()).filter(Boolean))] }
})
const groups = list('groups'), names = list('names'), topics = list('topics')
const jobs = computed(() => props.runtime?.jobs.filter(job => job.kind === 'ambient_chat' || job.trigger_reason === 'name') ?? [])
function reasonLabel(reason: string) { const key = 'qqbot.autonomous.reasons.' + reason; return te(key) ? t(key) : reason }
const fields = [
  { key: 'sample_percent', min: 0, max: 100 }, { key: 'cooldown_seconds', min: 10, max: 3600 },
  { key: 'min_messages', min: 0, max: 100 }, { key: 'member_cooldown_seconds', min: 0, max: 86400 },
  { key: 'hourly_replies', min: 0, max: 100 }, { key: 'daily_replies', min: 0, max: 1000 },
  { key: 'daily_calls', min: 0, max: 2000 }, { key: 'followup_seconds', min: 0, max: 120 },
  { key: 'start_hour', min: 0, max: 23 }, { key: 'end_hour', min: 0, max: 23 }, { key: 'max_chars', min: 20, max: 300 }
] as const
</script>
