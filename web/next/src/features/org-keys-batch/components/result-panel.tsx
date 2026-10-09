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
import { Download } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { CopyButton } from '@/components/copy-button';
import { Alert } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

import type { BatchResult } from '../api';
import { downloadCsv } from '../lib/download';
import { issuedToCsv } from '../lib/roster';

export function ResultPanel(props: {
  result: BatchResult;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const { created, skipped } = props.result;
  const csv = issuedToCsv(created);
  return (
    <div className='flex min-w-0 flex-col gap-4' data-testid='batch-result'>
      <Alert variant='destructive'>
        <div data-slot='alert-title' className='font-medium'>
          {t('Keys are shown only this once')}
        </div>
        <div data-slot='alert-description'>
          {t(
            'The plaintext keys cannot be retrieved again after you leave this page. Download the CSV now and store it securely.',
          )}
        </div>
      </Alert>
      <p className='text-sm'>
        {t('{{created}} created, {{skipped}} skipped (of {{requested}}).', {
          created: created.length,
          skipped: skipped.length,
          requested: props.result.requested,
        })}
      </p>
      <div className='flex flex-wrap gap-2'>
        <Button
          disabled={created.length === 0}
          onClick={() => downloadCsv('issued-keys.csv', csv)}
        >
          <Download />
          {t('Download keys CSV')}
        </Button>
        <CopyButton value={csv} aria-label={t('Copy CSV')} />
        <Button variant='outline' onClick={props.onDone}>
          {t('Done')}
        </Button>
      </div>
      {created.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Employee ref')}</TableHead>
              <TableHead>{t('Name')}</TableHead>
              <TableHead>{t('Key')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {created.map((r) => (
              <TableRow key={r.employee_ref}>
                <TableCell>{r.employee_ref}</TableCell>
                <TableCell>{r.name}</TableCell>
                <TableCell className='font-mono text-xs break-all'>
                  {r.key}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {skipped.length > 0 && (
        <div className='flex flex-col gap-1'>
          <h3 className='text-sm font-medium'>
            {t('Skipped: these employees already have a live key')}
          </h3>
          <ul className='text-muted-foreground text-sm'>
            {skipped.map((s) => (
              <li key={s.employee_ref}>
                {s.employee_ref} ({s.name})
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
