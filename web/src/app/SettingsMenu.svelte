<script lang="ts">
  import { DropdownMenu } from 'bits-ui';
  import {
    Menu,
    Sun,
    ChevronRight,
    Check,
    Monitor,
    Moon,
    Languages,
    LogOut,
  } from '@lucide/svelte';
  import {
    preferences,
    t,
    type Preference,
    type Language,
  } from '@/shared/settings.svelte';
  let { loggedIn, onlogout }: { loggedIn: boolean; onlogout: () => void } =
    $props();
</script>

<DropdownMenu.Root>
  <DropdownMenu.Trigger
    class="icon-button workspace-menu"
    aria-label={t('settings')}
    title={t('settings')}
  >
    <Menu size={16} />
  </DropdownMenu.Trigger>
  <DropdownMenu.Portal>
    <DropdownMenu.Content class="menu" sideOffset={10} align="end">
      <DropdownMenu.Sub>
        <DropdownMenu.SubTrigger class="menu-item">
          <Sun size={15} />{t('theme')}<ChevronRight
            size={14}
            class="menu-arrow"
          />
        </DropdownMenu.SubTrigger>
        <DropdownMenu.Portal>
          <DropdownMenu.SubContent class="menu" sideOffset={6}>
            <DropdownMenu.RadioGroup
              value={preferences.theme}
              onValueChange={(value) =>
                (preferences.theme = value as Preference)}
            >
              {#each ['auto', 'light', 'dark'] as value}<DropdownMenu.RadioItem
                  class="menu-item"
                  {value}
                >
                  <span class="check-slot">
                    {#if preferences.theme === value}<Check size={14} />{/if}
                  </span>{#if value === 'auto'}<Monitor
                      size={15}
                    />{:else if value === 'light'}<Sun size={15} />{:else}<Moon
                      size={15}
                    />{/if}{t(value as Preference)}
                </DropdownMenu.RadioItem>{/each}
            </DropdownMenu.RadioGroup>
          </DropdownMenu.SubContent>
        </DropdownMenu.Portal>
      </DropdownMenu.Sub>
      <DropdownMenu.Sub>
        <DropdownMenu.SubTrigger class="menu-item">
          <Languages size={15} />{t('language')}<ChevronRight
            size={14}
            class="menu-arrow"
          />
        </DropdownMenu.SubTrigger>
        <DropdownMenu.Portal>
          <DropdownMenu.SubContent class="menu" sideOffset={6}>
            <DropdownMenu.RadioGroup
              value={preferences.language}
              onValueChange={(value) =>
                (preferences.language = value as Language)}
            >
              {#each [{ value: 'auto', label: t('auto') }, { value: 'zh', label: '中文' }, { value: 'en', label: 'English' }] as item}<DropdownMenu.RadioItem
                  class="menu-item"
                  value={item.value}
                >
                  <span class="check-slot">
                    {#if preferences.language === item.value}<Check
                        size={14}
                      />{/if}
                  </span>{item.label}
                </DropdownMenu.RadioItem>{/each}
            </DropdownMenu.RadioGroup>
          </DropdownMenu.SubContent>
        </DropdownMenu.Portal>
      </DropdownMenu.Sub>
      {#if loggedIn}<DropdownMenu.Separator
          class="menu-separator"
        /><DropdownMenu.Item class="menu-item" onSelect={onlogout}>
          <LogOut size={15} />{t('logout')}
        </DropdownMenu.Item>{/if}
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>
