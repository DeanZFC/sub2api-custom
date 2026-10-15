<template>
  <AppLayout><div class="mx-auto max-w-5xl space-y-6">
    <div class="flex items-center justify-between gap-3"><h1 class="text-2xl font-semibold">{{ t('qqbot.runtime') }}</h1><button class="btn btn-secondary" :disabled="busy" @click="refresh">{{ t('common.refresh') }}</button></div>
    <p class="text-sm text-gray-500">{{ t('qqbot.runtimeHelp') }}</p>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <p v-if="busy && !runtime">{{ t('common.loading') }}</p>
      <section v-if="runtime" class="card space-y-4 p-6">
        <h2 class="text-lg font-semibold">{{ t('qqbot.runtime') }} · {{ t('qqbot.boundCount', { count: runtime.bindings }) }}</h2>
        <div class="overflow-x-auto"><table class="w-full min-w-[640px] text-left text-sm"><thead><tr><th>{{ t('qqbot.task') }}</th><th>{{ t('common.status') }}</th><th>Tokens</th><th>{{ t('common.error') }}</th></tr></thead><tbody><tr v-for="job in runtime.jobs" :key="job.id" class="border-t dark:border-dark-700"><td class="py-2"><code class="text-xs">{{ job.id }}</code><div class="text-gray-500">{{ job.kind }} · {{ job.created_at }}</div></td><td>{{ job.status }}</td><td>{{ job.input_tokens }} / {{ job.output_tokens }}</td><td class="max-w-sm break-words">{{ job.error || '—' }}</td></tr></tbody></table><p v-if="!runtime.jobs.length" class="py-4 text-gray-500">{{ t('common.noData') }}</p></div>
        <h3 class="font-semibold">{{ t('qqbot.rewardRecords') }}</h3>
        <div class="overflow-x-auto"><table class="w-full min-w-[640px] text-left text-sm"><thead><tr><th>User ID</th><th>{{ t('qqbot.rewardType') }}</th><th>USD</th><th>{{ t('qqbot.time') }}</th></tr></thead><tbody><tr v-for="reward in runtime.rewards" :key="reward.id" class="border-t dark:border-dark-700"><td class="py-2">{{ reward.user_id }}</td><td>{{ reward.kind }}</td><td>{{ reward.amount }}</td><td>{{ reward.created_at }}</td></tr></tbody></table><p v-if="!runtime.rewards.length" class="py-4 text-gray-500">{{ t('common.noData') }}</p></div>
      </section>
  </div></AppLayout>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { qqbotAPI, type QQBotRuntime } from '@/api/qqbot'
const { t } = useI18n()
const runtime = ref<QQBotRuntime | null>(null)
const busy = ref(false)
const error = ref('')
async function refresh() {
  busy.value = true; error.value = ''
  try { runtime.value = await qqbotAPI.runtime() }
  catch (e) { error.value = (e as Error).message || t('common.error') }
  finally { busy.value = false }
}
onMounted(refresh)
</script>
