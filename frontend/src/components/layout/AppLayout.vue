<template>
  <div class="bg-gray-50 dark:bg-dark-950" :class="contained ? 'app-layout-contained h-dvh overflow-hidden' : 'min-h-screen'">
    <!-- Background Decoration -->
    <div class="pointer-events-none fixed inset-0 bg-mesh-gradient"></div>

    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64', contained ? 'flex h-full min-h-0 flex-col' : 'min-h-screen']"
    >
      <!-- Header -->
      <AppHeader :class="{ 'shrink-0': contained }" />

      <!-- Main Content -->
      <main class="p-4 md:p-6 lg:p-8" :class="{ 'min-h-0 flex-1 overflow-hidden': contained }">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

// Opt in for pages whose panels own their scrolling, while keeping the
// ordinary document layout for all other routes.
withDefaults(defineProps<{ contained?: boolean }>(), { contained: false })

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>

<style scoped>
/* Mobile browser chrome can make 100vh taller than the visible viewport.
   Override the default body minimum only while a contained page is mounted. */
:global(body:has(.app-layout-contained)) {
  min-height: 100dvh;
}
</style>
