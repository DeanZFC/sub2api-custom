<template>
  <section data-passive-management>
    <GroupQualityTargetsPanel ref="targets" :visible="true" :groups="groups" :policy-id="targetPolicyId" @navigate="navigate" />
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { AdminGroup, AccountListItem } from '@/types'
import GroupQualityTargetsPanel from './GroupQualityTargetsPanel.vue'
defineProps<{ groups: AdminGroup[]; accounts: AccountListItem[]; targetPolicyId?: number; advancedPolicy?: boolean }>()
const emit = defineEmits<{ navigate: [tab: string, policy?: number, account?: number, phase?: string, advanced?: boolean] }>()
const targets = ref<InstanceType<typeof GroupQualityTargetsPanel> | null>(null)
function navigate(tab: string, policy?: number, account?: number, phase?: string, advanced?: boolean) { emit('navigate', tab, policy, account, phase, advanced) }
async function refresh() { await targets.value?.refresh() }
defineExpose({ refresh })
</script>
