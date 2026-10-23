import { useRef, useState, type FocusEvent, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconChevronDown } from '@/components/ui/icons';
import styles from '@/pages/UsagePage.module.scss';
import { API_KEY_SORT_KEYS, type ApiKeySortKey } from './ApiKeySettingsCard';

// 单选排序菜单：触发按钮显示当前排序，选中后立即关闭。
// 不复用 RequestEventsDetailsCard 的 shouldCloseMenuOnFocusLeave：会带入 i18n 副作用，mock 测试缺 initReactI18next 会炸。
export function ApiKeySortMenu({ value, onChange }: { value: ApiKeySortKey; onChange: (next: ApiKeySortKey) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const menuLabel = t('usage_stats.api_key_settings_sort');
  const optionLabel = (key: ApiKeySortKey) => t(`usage_stats.api_key_settings_sort_${key}`);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // 只有菜单自己在打开状态下才消费这次 Escape：调用 stopPropagation 阻止它继续冒泡到
    // 展开卡片在 window 上挂的 Escape 监听，否则一次按键会把菜单和卡片一起收起。
    // 菜单关闭时什么都不做，让事件正常冒泡，Escape 仍能收起展开的卡片。
    if (event.key === 'Escape' && open) {
      event.stopPropagation();
      setOpen(false);
      // Button 不转发 ref，用包裹节点上的 data 属性找回触发按钮，把焦点还给它。
      wrapperRef.current?.querySelector<HTMLButtonElement>('[data-api-key-sort-trigger]')?.focus();
    }
  };

  const handleBlur = (event: FocusEvent<HTMLDivElement>) => {
    const nextFocus = event.relatedTarget;
    const staysInside = nextFocus instanceof Node && event.currentTarget.contains(nextFocus);
    if (!staysInside) {
      setOpen(false);
    }
  };

  return (
    <div ref={wrapperRef} className={styles.apiKeySortMenu} onKeyDown={handleKeyDown} onBlur={handleBlur}>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className={`${styles.apiKeyVisibilityToggle} ${styles.apiKeySortToggle}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`${menuLabel}: ${optionLabel(value)}`}
        title={menuLabel}
        onClick={() => setOpen((current) => !current)}
        data-api-key-sort-trigger
      >
        {optionLabel(value)}
        <IconChevronDown size={12} aria-hidden="true" />
      </Button>
      {open && (
        <div className={styles.apiKeySortDropdown} role="menu" aria-label={menuLabel}>
          {API_KEY_SORT_KEYS.map((key) => (
            <button
              key={key}
              type="button"
              role="menuitemradio"
              aria-checked={key === value}
              data-api-key-sort-option={key}
              onClick={() => {
                setOpen(false);
                onChange(key);
              }}
            >
              {optionLabel(key)}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
