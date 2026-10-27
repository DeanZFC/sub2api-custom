<template>
  <section class="space-y-5" data-passive-management>
    <p class="text-sm text-gray-500">{{ t('admin.tests.scoring.passiveIntro') }}</p>
    <ChannelQualityScoreSettings ref="scoring" />
    <GroupQualityTargetsPanel ref="targets" :visible="true" :groups="groups" :policy-id="targetPolicyId" @navigate="navigate" />
    <details :open="expanded" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-advanced-protection @toggle="expanded = ($event.target as HTMLDetailsElement).open">
      <summary class="cursor-pointer text-sm font-medium">{{ t('admin.tests.scoring.advancedProtection') }}</summary>
      <p class="my-4 text-sm text-gray-500">{{ t('admin.tests.scoring.advancedProtectionHint') }}</p>
      <PassiveQualityPanel v-if="expanded" ref="advanced" tab="passive" :integrated="true" :groups="groups" :accounts="accounts" @tab="navigate" />
    </details>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminGroup, AccountListItem } from '@/types'
import ChannelQualityScoreSettings from './ChannelQualityScoreSettings.vue'
import GroupQualityTargetsPanel from './GroupQualityTargetsPanel.vue'
import PassiveQualityPanel from './PassiveQualityPanel.vue'
const props = defineProps<{ groups: AdminGroup[]; accounts: AccountListItem[]; targetPolicyId?: number; advancedPolicy?: boolean }>()
const emit = defineEmits<{ navigate: [tab: string, policy?: number, account?: number, phase?: string, advanced?: boolean] }>()
const { t } = useI18n()
const expanded = ref(!!props.advancedPolicy)
const scoring = ref<InstanceType<typeof ChannelQualityScoreSettings> | null>(null)
const targets = ref<InstanceType<typeof GroupQualityTargetsPanel> | null>(null)
const advanced = ref<InstanceType<typeof PassiveQualityPanel> | null>(null)
function navigate(tab: string, policy?: number, account?: number, phase?: string, advanced?: boolean) { emit('navigate', tab, policy, account, phase, advanced) }
async function refresh() { await Promise.all([scoring.value?.refresh(), targets.value?.refresh(), advanced.value?.refresh()]) }
watch(() => props.advancedPolicy, value => { if (value) expanded.value = true })
defineExpose({ refresh })
</script>
