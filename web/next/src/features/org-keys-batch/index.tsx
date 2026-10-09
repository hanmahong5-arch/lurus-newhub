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
import { useMutation } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { PageHeader } from '@/components/layout/page-header';
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
import { Textarea } from '@/components/ui/textarea';
import { toApiError } from '@/lib/api';
import { useMoneyConfig } from '@/lib/status';

import { batchCreateTokens, type BatchResult } from './api';
import { ResultPanel } from './components/result-panel';
import { downloadCsv } from './lib/download';
import {
  MAX_ROWS,
  TEMPLATE_CSV,
  buildItems,
  parseRoster,
  validateRows,
  type RowIssue,
} from './lib/roster';

const PAYER_NOT_SET = 'payer_not_set';

export function OrgKeysBatchPage() {
  const { t } = useTranslation();
  const money = useMoneyConfig();
  const [text, setText] = useState('');
  const [result, setResult] = useState<BatchResult | null>(null);
  const [payerMissing, setPayerMissing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const parsed = useMemo(
    () => (text.trim() === '' ? null : parseRoster(text)),
    [text],
  );
  const rows = useMemo(
    () => (parsed?.ok ? validateRows(parsed.rows, money) : []),
    [parsed, money],
  );
  const invalidCount = rows.filter((r) => r.issues.length > 0).length;
  const canSubmit = rows.length > 0 && invalidCount === 0;

  const issueLabel: Record<RowIssue, string> = {
    ref_required: t('employee_ref is required'),
    ref_invalid: t(
      'employee_ref must be 1-64 characters of A-Z a-z 0-9 . _ @ : -',
    ),
    ref_duplicate: t('Duplicate employee_ref'),
    name_too_long: t('Name is longer than 50 bytes (a Chinese character takes 3)'),
    quota_invalid: t('Quota must be a positive amount'),
    quota_too_large: t('Quota exceeds the maximum allowed'),
    quota_unavailable: t('Quota cannot be converted: unit price unavailable'),
  };

  const parseError = (() => {
    if (parsed === null || parsed.ok) return null;
    if (parsed.reason === 'too_many_rows') {
      return t('Too many rows: {{count}} (max {{max}}).', {
        count: parsed.count,
        max: MAX_ROWS,
      });
    }
    if (parsed.reason === 'no_employee_ref_column') {
      return t('The first line must be a header containing employee_ref.');
    }
    return t('No data rows found.');
  })();

  const submit = useMutation({
    mutationFn: () => batchCreateTokens(buildItems(rows)),
    onMutate: () => {
      setError(null);
      setPayerMissing(false);
    },
    onSuccess: (r) => {
      setResult(r);
      setText('');
    },
    onError: (e) => {
      const err = toApiError(e);
      if (err.status === 409 && err.code === PAYER_NOT_SET) {
        setPayerMissing(true);
      } else {
        setError(err.message);
      }
    },
  });

  const onFile = async (file: File | undefined) => {
    if (file) setText(await file.text());
  };

  if (result !== null) {
    return (
      <div className='flex min-w-0 flex-col gap-4'>
        <PageHeader title={t('Bulk Key Issuing')} />
        <ResultPanel result={result} onDone={() => setResult(null)} />
      </div>
    );
  }

  return (
    <div className='flex min-w-0 flex-col gap-4' data-testid='batch-form'>
      <PageHeader
        title={t('Bulk Key Issuing')}
        description={t(
          'Issue one key per employee from a CSV roster (up to {{max}} rows, all-or-nothing).',
          { max: MAX_ROWS },
        )}
      />
      <Alert>
        <div data-slot='alert-description' className='flex flex-col gap-1'>
          <span>
            {t(
              'Columns: employee_ref (required), name, dept_external_code, quota. A blank quota means unlimited.',
            )}
          </span>
          <span>
            {t(
              'dept_external_code is not accepted by the bulk endpoint and is ignored. Quota is in your display currency.',
            )}
          </span>
          <span>
            {t(
              'Re-uploading is safe: employees that already have a live key are skipped, not re-issued. Bulk keys are never trusted-identity-header keys.',
            )}
          </span>
        </div>
      </Alert>
      {payerMissing && (
        <Alert variant='destructive' data-testid='payer-missing'>
          <div data-slot='alert-description' className='flex flex-col gap-2'>
            <span>{t('Please set a payer on the Members page first.')}</span>
            <Link to='/org/members' className='underline'>
              {t('Go to Members')}
            </Link>
          </div>
        </Alert>
      )}
      {error !== null && (
        <Alert variant='destructive' data-testid='submit-error'>
          <div data-slot='alert-description'>{error}</div>
        </Alert>
      )}
      <div className='flex flex-wrap items-center gap-2'>
        <input
          type='file'
          accept='.csv,text/csv'
          aria-label={t('Upload CSV')}
          onChange={(e) => void onFile(e.target.files?.[0])}
        />
        <Button
          variant='outline'
          onClick={() => downloadCsv('roster-template.csv', TEMPLATE_CSV)}
        >
          {t('Download template')}
        </Button>
      </div>
      <Textarea
        aria-label={t('Roster CSV')}
        rows={8}
        className='font-mono text-xs'
        placeholder='employee_ref,name,dept_external_code,quota'
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      {parseError !== null && (
        <Alert variant='destructive' data-testid='parse-error'>
          <div data-slot='alert-description'>{parseError}</div>
        </Alert>
      )}
      {rows.length > 0 && (
        <div className='flex min-w-0 flex-col gap-2'>
          <p className='text-sm' data-testid='preview-summary'>
            {invalidCount > 0
              ? t('{{count}} rows, {{invalid}} with errors', {
                  count: rows.length,
                  invalid: invalidCount,
                })
              : t('{{count}} rows ready', { count: rows.length })}
          </p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>#</TableHead>
                <TableHead>{t('Employee ref')}</TableHead>
                <TableHead>{t('Name')}</TableHead>
                <TableHead>{t('Quota')}</TableHead>
                <TableHead>{t('Check')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.line}>
                  <TableCell>{r.line}</TableCell>
                  <TableCell>{r.employeeRef}</TableCell>
                  <TableCell>
                    {r.name === '' ? r.employeeRef : r.name}
                  </TableCell>
                  <TableCell>
                    {r.quotaText === '' ? t('Unlimited') : r.quotaText}
                  </TableCell>
                  <TableCell className='text-destructive'>
                    {r.issues.map((i) => issueLabel[i]).join('; ')}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <div>
        <Button
          disabled={!canSubmit || submit.isPending}
          onClick={() => submit.mutate()}
        >
          {submit.isPending ? t('Issuing...') : t('Issue keys')}
        </Button>
      </div>
    </div>
  );
}
