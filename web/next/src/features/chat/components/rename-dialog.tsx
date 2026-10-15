/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';

import { isValidTitle, MAX_TITLE_CHARS } from '../lib/chat';
import type { ChatSessionSummary } from '../types';

function RenameForm(props: {
  session: ChatSessionSummary;
  pending: boolean;
  onSubmit: (title: string) => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [title, setTitle] = useState(props.session.title);
  const valid = isValidTitle(title);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (valid && !props.pending) props.onSubmit(title.trim());
  };

  return (
    <form onSubmit={submit} className='grid gap-4'>
      <DialogHeader>
        <DialogTitle>{t('Rename conversation')}</DialogTitle>
        <DialogDescription>
          {t('Give this saved conversation a name you will recognise.')}
        </DialogDescription>
      </DialogHeader>
      <Input
        autoFocus
        aria-label={t('Conversation name')}
        maxLength={MAX_TITLE_CHARS}
        value={title}
        onChange={(event) => setTitle(event.target.value)}
      />
      <DialogFooter>
        <Button
          type='button'
          variant='outline'
          disabled={props.pending}
          onClick={props.onCancel}
        >
          {t('Cancel')}
        </Button>
        <Button type='submit' disabled={!valid || props.pending}>
          {t('Save')}
        </Button>
      </DialogFooter>
    </form>
  );
}

export function RenameDialog(props: {
  session: ChatSessionSummary | null;
  pending: boolean;
  onSubmit: (session: ChatSessionSummary, title: string) => void;
  onClose: () => void;
}) {
  const { session } = props;
  return (
    <Dialog
      open={session !== null}
      onOpenChange={(open) => {
        if (!open && !props.pending) props.onClose();
      }}
    >
      <DialogContent className='sm:max-w-md'>
        {session !== null && (
          <RenameForm
            // Remount per conversation so the field starts from its own title.
            key={session.id}
            session={session}
            pending={props.pending}
            onSubmit={(title) => props.onSubmit(session, title)}
            onCancel={props.onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
