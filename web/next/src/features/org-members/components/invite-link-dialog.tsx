import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface InviteLinkDialogProps {
  link: string
  code: string
  onClose: () => void
}

/** Shown once after creating: the full code is never returned by the list. */
export function InviteLinkDialog(props: InviteLinkDialogProps) {
  const { t } = useTranslation()
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Invitation created')}</DialogTitle>
          <DialogDescription>
            {t(
              'Copy the link now. For security the full code is not shown again after you close this window.'
            )}
          </DialogDescription>
        </DialogHeader>
        <div className='grid gap-3'>
          <div className='flex items-center gap-2'>
            <code
              className='bg-muted min-w-0 flex-1 rounded px-2 py-1.5 text-xs break-all'
              data-testid='invite-link'
            >
              {props.link}
            </code>
            <CopyButton value={props.link} aria-label={t('Copy invitation link')} />
          </div>
          <div className='flex items-center gap-2 text-sm'>
            <span className='text-muted-foreground'>{t('Code')}</span>
            <code className='break-all'>{props.code}</code>
            <CopyButton value={props.code} aria-label={t('Copy invitation code')} />
          </div>
        </div>
        <DialogFooter>
          <Button onClick={props.onClose}>{t('Done')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
