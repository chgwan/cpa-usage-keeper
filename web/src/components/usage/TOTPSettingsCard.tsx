import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { QRCodeSVG } from 'qrcode.react';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import {
  confirmTOTP,
  disableTOTP,
  fetchTOTPStatus,
  setupTOTP,
  type TOTPSetupResponse,
  type TOTPStatusResponse,
} from '@/lib/api';
import styles from '@/pages/UsagePage.module.scss';

export function isTOTPDisableFormValid(password: string, code: string): boolean {
  return Boolean(password.trim() && code.trim());
}

export function TOTPSettingsCard() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<TOTPStatusResponse | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [setup, setSetup] = useState<TOTPSetupResponse | null>(null);
  const [setupCode, setSetupCode] = useState('');
  const [setupError, setSetupError] = useState('');
  const [busy, setBusy] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  const [disablePassword, setDisablePassword] = useState('');
  const [disableCode, setDisableCode] = useState('');
  const [disableError, setDisableError] = useState('');

  useEffect(() => {
    let active = true;
    fetchTOTPStatus()
      .then((next) => { if (active) setStatus(next); })
      .catch(() => { if (active) setLoadFailed(true); });
    return () => { active = false; };
  }, []);

  const handleEnable = useCallback(async () => {
    setBusy(true);
    setSetupError('');
    try {
      setSetup(await setupTOTP());
    } catch {
      setSetupError(t('usage_stats.totp_error_setup_failed'));
    } finally {
      setBusy(false);
    }
  }, [t]);

  const handleConfirm = useCallback(async () => {
    setBusy(true);
    setSetupError('');
    try {
      await confirmTOTP(setupCode);
      setStatus({ enabled: true, pending: false });
      setSetup(null);
      setSetupCode('');
    } catch {
      setSetupError(t('usage_stats.totp_error_invalid_code'));
    } finally {
      setBusy(false);
    }
  }, [setupCode, t]);

  const handleDisable = useCallback(async () => {
    setBusy(true);
    setDisableError('');
    try {
      await disableTOTP(disablePassword, disableCode);
      setStatus({ enabled: false, pending: false });
      setDisableOpen(false);
      setDisablePassword('');
      setDisableCode('');
    } catch {
      setDisableError(t('usage_stats.totp_error_invalid_credentials'));
    } finally {
      setBusy(false);
    }
  }, [disableCode, disablePassword, t]);

  const enabled = status?.enabled ?? false;

  return (
    <Card>
      <div className={styles.totpRow}>
        <div>
          <h3>{t('usage_stats.totp_title')}</h3>
          <p className={styles.totpHint}>
            {enabled ? t('usage_stats.totp_status_enabled') : t('usage_stats.totp_status_disabled')}
          </p>
        </div>
        {loadFailed ? (
          <span>{t('usage_stats.totp_error_load_failed')}</span>
        ) : enabled ? (
          <Button type="button" onClick={() => setDisableOpen(true)}>{t('usage_stats.totp_disable')}</Button>
        ) : (
          <Button type="button" loading={busy} onClick={() => void handleEnable()}>{t('usage_stats.totp_enable')}</Button>
        )}
      </div>

      {setupError && !setup && <p className={styles.totpHint}>{setupError}</p>}

      <Modal
        open={Boolean(setup)}
        title={t('usage_stats.totp_setup_title')}
        onClose={() => { setSetup(null); setSetupCode(''); setSetupError(''); }}
        closeDisabled={busy}
      >
        <div className={styles.totpFieldStack}>
          <p>{t('usage_stats.totp_setup_hint')}</p>
          <div className={styles.totpQRWrap}>
            {setup && <QRCodeSVG value={setup.otpauth_uri} size={168} />}
          </div>
          <div className={styles.totpSecretWrap}>
            <div className={styles.totpSecretLabel}>{t('usage_stats.totp_secret_label')}</div>
            <div className={styles.totpSecret}>{setup?.secret}</div>
          </div>
          <Input
            type="text"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={8}
            label={t('usage_stats.totp_code_label')}
            value={setupCode}
            onChange={(event) => setSetupCode(event.target.value)}
            disabled={busy}
          />
          {setupError && <p>{setupError}</p>}
          <Button type="button" fullWidth disabled={!setupCode.trim() || busy} onClick={() => void handleConfirm()}>
            {t('usage_stats.totp_confirm')}
          </Button>
        </div>
      </Modal>

      <Modal
        open={disableOpen}
        title={t('usage_stats.totp_disable_title')}
        onClose={() => {
          setDisableOpen(false);
          setDisablePassword('');
          setDisableCode('');
          setDisableError('');
        }}
        closeDisabled={busy}
      >
        <div className={styles.totpFieldStack}>
          <Input
            type="password"
            autoComplete="current-password"
            label={t('usage_stats.totp_password_label')}
            value={disablePassword}
            onChange={(event) => setDisablePassword(event.target.value)}
            disabled={busy}
          />
          <Input
            type="text"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={8}
            label={t('usage_stats.totp_code_label')}
            value={disableCode}
            onChange={(event) => setDisableCode(event.target.value)}
            disabled={busy}
          />
          {disableError && <p>{disableError}</p>}
          <Button
            type="button"
            fullWidth
            disabled={!isTOTPDisableFormValid(disablePassword, disableCode) || busy}
            onClick={() => void handleDisable()}
          >
            {t('usage_stats.totp_disable_confirm')}
          </Button>
        </div>
      </Modal>
    </Card>
  );
}
