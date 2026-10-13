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
import { Copy, Eye, EyeOff, Eraser, KeyRound, Loader2 } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { copyToClipboard } from '@/lib/copy-to-clipboard'

import {
  ADD_MODE_OPTIONS,
  BASE_URL_REQUIRED_TYPES,
  CHANNEL_DEFAULT_BASE_URLS,
  CHANNEL_TYPE_AWS,
  CHANNEL_TYPE_OPENROUTER,
  CHANNEL_TYPE_VERTEX,
  CHANNEL_TYPE_WARNINGS,
  FIELD_DESCRIPTIONS,
  FIELD_PLACEHOLDERS,
  MULTI_KEY_MODES,
  OPENAI_FIELD_PASSTHROUGH_TYPES,
  OTHER_REQUIRED_TYPES,
} from '../../constants'
import {
  deduplicateKeys,
  formatModelsArray,
  getKeyPromptForType,
  parseModelsString,
  type ChannelFormValues,
} from '../../lib'
import { ModelMappingEditor } from '../model-mapping-editor'

export type ChannelForm = UseFormReturn<ChannelFormValues>

/** What `other` means for the types that require it. */
const OTHER_FIELD: Record<
  number,
  { label: string; placeholder: string; hint?: string }
> = {
  3: {
    label: 'API version',
    placeholder: '2024-06-01',
    hint: 'Default API version used for this deployment.',
  },
  18: { label: 'Version', placeholder: 'v2.1' },
  39: { label: 'Account ID', placeholder: 'Cloudflare account id' },
  41: {
    label: 'Deployment region',
    placeholder: '{"default": "us-central1", "region-b": "us-east1"}',
    hint: 'A JSON object with a "default" region.',
  },
  49: { label: 'Bot ID', placeholder: 'Coze bot id' },
}

function SwitchField(props: {
  form: ChannelForm
  name:
    | 'force_format'
    | 'thinking_to_content'
    | 'system_prompt_override'
    | 'data_collection_deny'
    | 'is_enterprise_account'
    | 'allow_service_tier'
    | 'disable_store'
    | 'allow_safety_identifier'
    | 'auto_ban'
    | 'pass_through_body_enabled'
    | 'batch_add_set_key_prefix_2_name'
  label: string
  description?: string
  /** Called with the next value before it is stored; return false to refuse. */
  onBeforeChange?: (next: boolean) => boolean
  numeric?: boolean
}) {
  const { t } = useTranslation()
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem className={sideDrawerSwitchItemClassName()}>
          <div className='flex flex-col gap-0.5'>
            <FormLabel>{t(props.label)}</FormLabel>
            {props.description && (
              <FormDescription className='text-xs'>
                {t(props.description)}
              </FormDescription>
            )}
          </div>
          <FormControl>
            <Switch
              checked={
                props.numeric ? Number(field.value) === 1 : field.value === true
              }
              onCheckedChange={(checked) => {
                if (props.onBeforeChange && !props.onBeforeChange(checked)) {
                  return
                }
                if (props.numeric) field.onChange(Number(checked))
                else field.onChange(checked)
              }}
            />
          </FormControl>
        </FormItem>
      )}
    />
  )
}

function NumberField(props: {
  form: ChannelForm
  name: 'priority' | 'weight'
  label: string
  description: string
  min?: number
}) {
  const { t } = useTranslation()
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{t(props.label)}</FormLabel>
          <FormControl>
            <Input
              type='number'
              inputMode='numeric'
              min={props.min}
              value={field.value ?? 0}
              onChange={(e) => {
                const next = Number.parseInt(e.target.value, 10)
                field.onChange(Number.isFinite(next) ? next : 0)
              }}
              onBlur={field.onBlur}
              name={field.name}
              ref={field.ref}
            />
          </FormControl>
          <FormDescription>{t(props.description)}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

function SelectField(props: {
  form: ChannelForm
  name:
    | 'multi_key_mode'
    | 'multi_key_type'
    | 'key_mode'
    | 'vertex_key_type'
    | 'aws_key_type'
  label: string
  options: ReadonlyArray<{ value: string; label: string }>
  description?: string
}) {
  const { t } = useTranslation()
  const items = props.options.map((o) => ({ value: o.value, label: t(o.label) }))
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{t(props.label)}</FormLabel>
          <Select
            items={items}
            value={field.value ?? ''}
            onValueChange={field.onChange}
          >
            <FormControl>
              <SelectTrigger className='w-full'>
                <SelectValue />
              </SelectTrigger>
            </FormControl>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {items.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          {props.description && (
            <FormDescription>{t(props.description)}</FormDescription>
          )}
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

function JsonField(props: {
  form: ChannelForm
  name: 'param_override' | 'header_override' | 'status_code_mapping'
  label: string
  description: string
  placeholder: string
}) {
  const { t } = useTranslation()
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field, fieldState }) => (
        <FormItem>
          <FormLabel>{t(props.label)}</FormLabel>
          <FormControl>
            <JsonCodeEditor
              value={field.value ?? ''}
              onChange={field.onChange}
              onBlur={field.onBlur}
              name={field.name}
              textareaRef={field.ref}
              placeholder={props.placeholder}
              ariaLabel={t(props.label)}
              aria-invalid={fieldState.invalid || undefined}
              heightClassName='h-40 min-h-40 max-h-40'
            />
          </FormControl>
          <FormDescription>{t(props.description)}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

// ============================================================================
// Basic
// ============================================================================

export function BasicSection(props: {
  form: ChannelForm
  groupOptions: string[]
  type: number
}) {
  const { t } = useTranslation()
  const { form } = props
  const warning = CHANNEL_TYPE_WARNINGS[props.type]
  const groupOptions = useMemo(
    () => props.groupOptions.map((g) => ({ label: g, value: g })),
    [props.groupOptions]
  )
  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Basic information')}
        description={t('Name, status and who can use this channel.')}
      />
      {warning && (
        <p className='text-warning text-xs' role='note'>
          {t(warning)}
        </p>
      )}
      <FormField
        control={form.control}
        name='name'
        render={({ field }) => (
          <FormItem>
            <FormLabel required>{t('Name')}</FormLabel>
            <FormControl>
              <Input placeholder={t(FIELD_PLACEHOLDERS.NAME)} {...field} />
            </FormControl>
            <FormDescription>{t(FIELD_DESCRIPTIONS.NAME)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='status'
        render={({ field }) => (
          <FormItem className={sideDrawerSwitchItemClassName()}>
            <div className='flex flex-col gap-0.5'>
              <FormLabel>{t('Enabled')}</FormLabel>
              <FormDescription className='text-xs'>
                {t('Enable or disable this channel')}
              </FormDescription>
            </div>
            <FormControl>
              <Switch
                checked={field.value === 1}
                onCheckedChange={(checked) => field.onChange(checked ? 1 : 2)}
              />
            </FormControl>
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='group'
        render={({ field }) => (
          <FormItem>
            <FormLabel required>{t('Groups')}</FormLabel>
            <FormControl>
              <MultiSelect
                options={groupOptions}
                selected={field.value}
                onChange={field.onChange}
                placeholder={t(FIELD_PLACEHOLDERS.GROUP)}
                allowCreate
              />
            </FormControl>
            <FormDescription>{t(FIELD_DESCRIPTIONS.GROUP)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <div className='grid gap-4 sm:grid-cols-2'>
        <FormField
          control={form.control}
          name='tag'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Tag')}</FormLabel>
              <FormControl>
                <Input placeholder={t(FIELD_PLACEHOLDERS.TAG)} {...field} />
              </FormControl>
              <FormDescription>{t(FIELD_DESCRIPTIONS.TAG)}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='remark'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Remark')}</FormLabel>
              <FormControl>
                <Input placeholder={t(FIELD_PLACEHOLDERS.REMARK)} {...field} />
              </FormControl>
              <FormDescription>{t(FIELD_DESCRIPTIONS.REMARK)}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
    </SideDrawerSection>
  )
}

// ============================================================================
// Connection (base URL and type-specific fields)
// ============================================================================

export function ConnectionSection(props: { form: ChannelForm; type: number }) {
  const { t } = useTranslation()
  const { form, type } = props
  const defaultBaseUrl = CHANNEL_DEFAULT_BASE_URLS[type]
  const otherConfig = OTHER_FIELD[type]
  const showOther = OTHER_REQUIRED_TYPES.has(type) && otherConfig !== undefined
  const baseUrlRequired = BASE_URL_REQUIRED_TYPES.has(type)

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Connection')}
        description={t('Where requests are sent.')}
      />
      <FormField
        control={form.control}
        name='base_url'
        render={({ field }) => (
          <FormItem>
            <FormLabel required={baseUrlRequired}>{t('Base URL')}</FormLabel>
            <FormControl>
              <Input
                placeholder={defaultBaseUrl ?? t(FIELD_PLACEHOLDERS.BASE_URL)}
                {...field}
              />
            </FormControl>
            <FormDescription>
              {defaultBaseUrl
                ? t('Leave empty to use the provider default: {{url}}', {
                    url: defaultBaseUrl,
                  })
                : t(FIELD_DESCRIPTIONS.BASE_URL)}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      {showOther && otherConfig && (
        <FormField
          control={form.control}
          name='other'
          render={({ field }) => (
            <FormItem>
              <FormLabel required>{t(otherConfig.label)}</FormLabel>
              <FormControl>
                {type === CHANNEL_TYPE_VERTEX ? (
                  <Textarea
                    rows={3}
                    className='font-mono text-xs'
                    placeholder={t(otherConfig.placeholder)}
                    {...field}
                  />
                ) : (
                  <Input placeholder={t(otherConfig.placeholder)} {...field} />
                )}
              </FormControl>
              {otherConfig.hint && (
                <FormDescription>{t(otherConfig.hint)}</FormDescription>
              )}
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      {type === 1 && (
        <FormField
          control={form.control}
          name='openai_organization'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('OpenAI Organization')}</FormLabel>
              <FormControl>
                <Input placeholder='org-...' {...field} />
              </FormControl>
              <FormDescription>{t(FIELD_DESCRIPTIONS.OPENAI_ORG)}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      {type === CHANNEL_TYPE_VERTEX && (
        <SelectField
          form={form}
          name='vertex_key_type'
          label='Key type'
          options={[
            { value: 'json', label: 'Service account JSON' },
            { value: 'api_key', label: 'API key' },
          ]}
        />
      )}
      {type === CHANNEL_TYPE_AWS && (
        <SelectField
          form={form}
          name='aws_key_type'
          label='Key type'
          options={[
            { value: 'ak_sk', label: 'Access key and secret' },
            { value: 'api_key', label: 'API key' },
          ]}
        />
      )}
      {type === 3 && (
        <FormField
          control={form.control}
          name='azure_responses_version'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Responses API version')}</FormLabel>
              <FormControl>
                <Input placeholder='preview' {...field} />
              </FormControl>
              <FormDescription>
                {t('API version used for the Responses endpoint (optional).')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      {type === CHANNEL_TYPE_OPENROUTER && (
        <SwitchField
          form={form}
          name='is_enterprise_account'
          label='Enterprise account'
          description='The upstream account is an enterprise account.'
        />
      )}
    </SideDrawerSection>
  )
}

// ============================================================================
// Authentication (key, add modes, multi-key)
// ============================================================================

export function AuthSection(props: {
  form: ChannelForm
  type: number
  isEditing: boolean
  isMultiKey: boolean
  /** Root only: reveal the stored key behind a verification prompt. */
  canReveal: boolean
  revealedKey: string | null
  isRevealing: boolean
  onReveal: () => void
}) {
  const { t } = useTranslation()
  const { form, type } = props
  const inputId = useId()
  const [showKey, setShowKey] = useState(false)
  const addMode = form.watch('multi_key_mode') ?? 'single'
  const vertexKeyType = form.watch('vertex_key_type')
  const keyValue = form.watch('key')
  const multiline =
    props.isMultiKey ||
    (!props.isEditing && addMode !== 'single') ||
    (type === CHANNEL_TYPE_VERTEX && vertexKeyType !== 'api_key')
  const addModes = ADD_MODE_OPTIONS.filter(
    (o) => !(type === CHANNEL_TYPE_VERTEX && vertexKeyType === 'api_key' && o.value !== 'single')
  )

  let placeholder = t(getKeyPromptForType(type))
  if (props.isEditing) placeholder = t('Leave empty to keep existing key')
  else if (addMode !== 'single') {
    placeholder = t('One key per line')
  }

  const handleDedupe = () => {
    const { deduplicatedText, removedCount } = deduplicateKeys(keyValue)
    if (removedCount === 0) {
      toast.info(t('No duplicate keys found'))
      return
    }
    form.setValue('key', deduplicatedText, {
      shouldDirty: true,
      shouldValidate: true,
    })
    toast.success(t('Removed {{count}} duplicate key(s)', { count: removedCount }))
  }

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Authentication')}
        description={t('API key and how multiple keys are used.')}
      />
      {!props.isEditing && (
        <SelectField
          form={form}
          name='multi_key_mode'
          label='Add Mode'
          options={addModes}
        />
      )}
      {!props.isEditing && addMode === 'multi_to_single' && (
        <SelectField
          form={form}
          name='multi_key_type'
          label='Key rotation'
          options={MULTI_KEY_MODES}
          description={FIELD_DESCRIPTIONS.MULTI_KEY_MODE}
        />
      )}
      {!props.isEditing && addMode === 'batch' && (
        <SwitchField
          form={form}
          name='batch_add_set_key_prefix_2_name'
          label='Append key prefix to name'
          description='Name each channel with the first characters of its key.'
        />
      )}
      {props.isEditing && props.isMultiKey && (
        <div className='grid gap-4 sm:grid-cols-2'>
          <SelectField
            form={form}
            name='multi_key_type'
            label='Key rotation'
            options={MULTI_KEY_MODES}
            description={FIELD_DESCRIPTIONS.MULTI_KEY_MODE}
          />
          <SelectField
            form={form}
            name='key_mode'
            label='When adding keys'
            options={[
              { value: 'replace', label: 'Replace all keys' },
              { value: 'append', label: 'Append to existing keys' },
            ]}
          />
        </div>
      )}
      <FormField
        control={form.control}
        name='key'
        render={({ field }) => (
          <FormItem>
            <FormLabel required={!props.isEditing}>{t('API Key')}</FormLabel>
            <FormControl>
              {multiline ? (
                <Textarea
                  rows={4}
                  className='font-mono text-xs'
                  autoComplete='off'
                  spellCheck={false}
                  placeholder={placeholder}
                  {...field}
                />
              ) : (
                <div className='relative'>
                  <Input
                    id={inputId}
                    type={showKey ? 'text' : 'password'}
                    autoComplete='off'
                    spellCheck={false}
                    placeholder={placeholder}
                    className='pr-10'
                    {...field}
                  />
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    className='absolute top-1/2 right-1 -translate-y-1/2'
                    aria-label={showKey ? t('Hide') : t('Show')}
                    onClick={() => setShowKey((v) => !v)}
                  >
                    {showKey ? <EyeOff /> : <Eye />}
                  </Button>
                </div>
              )}
            </FormControl>
            <FormDescription>{t(FIELD_DESCRIPTIONS.KEY)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      {multiline && (
        <div>
          <Button type='button' variant='outline' size='sm' onClick={handleDedupe}>
            <Eraser data-icon='inline-start' />
            {t('Remove duplicate keys')}
          </Button>
        </div>
      )}
      {props.isEditing && props.canReveal && (
        <div className='grid gap-2'>
          {props.revealedKey === null ? (
            <div>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={props.onReveal}
                disabled={props.isRevealing}
              >
                {props.isRevealing ? (
                  <Loader2 data-icon='inline-start' className='animate-spin' />
                ) : (
                  <KeyRound data-icon='inline-start' />
                )}
                {t('Reveal current key')}
              </Button>
            </div>
          ) : (
            <>
              <Textarea
                readOnly
                rows={Math.min(6, props.revealedKey.split('\n').length + 1)}
                className='font-mono text-xs'
                aria-label={t('Current key')}
                value={props.revealedKey}
              />
              <div>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => {
                    void copyToClipboard(props.revealedKey ?? '')
                    toast.success(t('Copied'))
                  }}
                >
                  <Copy data-icon='inline-start' />
                  {t('Copy')}
                </Button>
              </div>
            </>
          )}
        </div>
      )}
    </SideDrawerSection>
  )
}

// ============================================================================
// Models and model mapping
// ============================================================================

export function ModelsSection(props: {
  form: ChannelForm
  modelCatalogue: string[]
  fetchedModels: string[]
  prefillGroups: Array<{ id: number; name: string; items: string[] }>
  canFetch: boolean
  onFetch: () => void
  onBatchMapping: () => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const { form } = props
  const modelsValue = form.watch('models')
  const mappingValue = form.watch('model_mapping') ?? ''
  const selected = useMemo(() => parseModelsString(modelsValue), [modelsValue])
  const options = useMemo(
    () =>
      [...new Set([...selected, ...props.fetchedModels, ...props.modelCatalogue])]
        .sort((a, b) => a.localeCompare(b))
        .map((m) => ({ label: m, value: m })),
    [selected, props.fetchedModels, props.modelCatalogue]
  )

  const setModels = (models: string[]) =>
    form.setValue('models', formatModelsArray(models), {
      shouldDirty: true,
      shouldValidate: true,
    })

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Models')}
        description={t('Which models this channel serves, and how they are renamed.')}
      />
      <FormField
        control={form.control}
        name='models'
        render={() => (
          <FormItem>
            <FormLabel required>{t('Models')}</FormLabel>
            <FormControl>
              <MultiSelect
                options={options}
                selected={selected}
                onChange={setModels}
                placeholder={t(FIELD_PLACEHOLDERS.MODELS)}
                allowCreate
                maxVisibleChips={30}
              />
            </FormControl>
            <FormDescription>{t(FIELD_DESCRIPTIONS.MODELS)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <div className='flex flex-wrap gap-2'>
        {props.canFetch && (
          <Button type='button' variant='outline' size='sm' onClick={props.onFetch}>
            {t('Fetch upstream models')}
          </Button>
        )}
        {props.prefillGroups.map((group) => (
          <Button
            key={group.id}
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setModels([...selected, ...group.items])}
          >
            {t('Add group: {{name}}', { name: group.name })}
          </Button>
        ))}
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={selected.length === 0}
          onClick={() => {
            void copyToClipboard(selected.join(','))
            toast.success(t('Copied'))
          }}
        >
          <Copy data-icon='inline-start' />
          {t('Copy models')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={selected.length === 0}
          onClick={() => setModels([])}
        >
          <Eraser data-icon='inline-start' />
          {t('Clear models')}
        </Button>
      </div>
      <FormField
        control={form.control}
        name='test_model'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Test model')}</FormLabel>
            <FormControl>
              <Input
                list={`${field.name}-options`}
                placeholder={t(FIELD_PLACEHOLDERS.TEST_MODEL)}
                {...field}
              />
            </FormControl>
            <datalist id={`${field.name}-options`}>
              {selected.map((m) => (
                <option key={m} value={m} />
              ))}
            </datalist>
            <FormDescription>{t(FIELD_DESCRIPTIONS.TEST_MODEL)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='model_mapping'
        render={({ fieldState }) => (
          <FormItem>
            <FormLabel>{t('Model redirect')}</FormLabel>
            <FormControl>
              <div aria-invalid={fieldState.invalid || undefined}>
                <ModelMappingEditor
                  value={mappingValue}
                  onChange={(next) =>
                    form.setValue('model_mapping', next, {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                  sourceModelOptions={selected}
                  targetModelOptions={[
                    ...new Set([...props.fetchedModels, ...selected]),
                  ]}
                  onBatchAdd={props.onBatchMapping}
                  disabled={props.disabled}
                />
              </div>
            </FormControl>
            <FormDescription>{t(FIELD_DESCRIPTIONS.MODEL_MAPPING)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </SideDrawerSection>
  )
}

// ============================================================================
// Routing and limits
// ============================================================================

export function RoutingSection(props: { form: ChannelForm }) {
  const { t } = useTranslation()
  const { form } = props
  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Routing')}
        description={t('How requests are spread across channels.')}
      />
      <div className='grid gap-4 sm:grid-cols-2'>
        <NumberField
          form={form}
          name='priority'
          label='Priority'
          description={FIELD_DESCRIPTIONS.PRIORITY}
        />
        <NumberField
          form={form}
          name='weight'
          label='Weight'
          description={FIELD_DESCRIPTIONS.WEIGHT}
          min={0}
        />
      </div>
      <SwitchField
        form={form}
        name='auto_ban'
        label='Auto disable on failure'
        description={FIELD_DESCRIPTIONS.AUTO_BAN}
        numeric
      />
      <JsonField
        form={form}
        name='status_code_mapping'
        label='Status code mapping'
        description={FIELD_DESCRIPTIONS.STATUS_CODE_MAPPING}
        placeholder={FIELD_PLACEHOLDERS.STATUS_CODE_MAPPING}
      />
    </SideDrawerSection>
  )
}

// ============================================================================
// Advanced (overrides, proxy, passthrough)
// ============================================================================

export function AdvancedSection(props: {
  form: ChannelForm
  type: number
  onPassThroughChange: (next: boolean) => boolean
}) {
  const { t } = useTranslation()
  const { form, type } = props
  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Advanced')}
        description={t('Request overrides, proxy and compatibility switches.')}
      />
      <JsonField
        form={form}
        name='param_override'
        label='Parameter override'
        description={FIELD_DESCRIPTIONS.PARAM_OVERRIDE}
        placeholder={FIELD_PLACEHOLDERS.PARAM_OVERRIDE}
      />
      <JsonField
        form={form}
        name='header_override'
        label='Header override'
        description={FIELD_DESCRIPTIONS.HEADER_OVERRIDE}
        placeholder={FIELD_PLACEHOLDERS.HEADER_OVERRIDE}
      />
      <FormField
        control={form.control}
        name='proxy'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Proxy')}</FormLabel>
            <FormControl>
              <Input placeholder='socks5://user:pass@host:1080' {...field} />
            </FormControl>
            <FormDescription>
              {t(
                'Outbound proxy for this channel (HTTP, HTTPS, SOCKS5 or SOCKS5H).'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <div className='grid gap-4 sm:grid-cols-2'>
        <FormField
          control={form.control}
          name='region'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Upstream region')}</FormLabel>
              <FormControl>
                <Input placeholder='eu' {...field} />
              </FormControl>
              <FormDescription>
                {t('Declared region, matched against a request provider filter.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
      <SwitchField
        form={form}
        name='data_collection_deny'
        label='Zero data retention'
        description='The upstream keeps no prompt or completion data.'
      />
      <SwitchField
        form={form}
        name='force_format'
        label='Force format'
        description='Force the response into the OpenAI format.'
      />
      <SwitchField
        form={form}
        name='thinking_to_content'
        label='Thinking to content'
        description='Merge reasoning output into the message content.'
      />
      <SwitchField
        form={form}
        name='pass_through_body_enabled'
        label='Request body passthrough'
        description='Forward the original request body without gateway rewriting.'
        onBeforeChange={props.onPassThroughChange}
      />
      <FormField
        control={form.control}
        name='system_prompt'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('System prompt')}</FormLabel>
            <FormControl>
              <Textarea rows={3} {...field} />
            </FormControl>
            <FormDescription>
              {t('Prepended to every request sent through this channel.')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <SwitchField
        form={form}
        name='system_prompt_override'
        label='Override client system prompt'
        description='Replace the client system prompt instead of prepending to it.'
      />
      {OPENAI_FIELD_PASSTHROUGH_TYPES.has(type) && (
        <>
          <SwitchField
            form={form}
            name='allow_service_tier'
            label='Allow service tier'
            description='Pass the service_tier field to the upstream (may change billing).'
          />
          <SwitchField
            form={form}
            name='disable_store'
            label='Disable store'
            description='Do not pass the store field to the upstream.'
          />
          <SwitchField
            form={form}
            name='allow_safety_identifier'
            label='Allow safety identifier'
            description='Pass the safety_identifier field to the upstream.'
          />
        </>
      )}
    </SideDrawerSection>
  )
}
