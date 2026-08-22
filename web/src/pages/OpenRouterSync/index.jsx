/*
Copyright (C) 2025 QuantumNous

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
import React, { useEffect, useState, useCallback } from 'react';
import {
  Button,
  Card,
  Form,
  Modal,
  Notification,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { API } from '../../helpers/api';
import ApiPoolCard from './ApiPoolCard';
import i18next from '../../i18n/i18n';

const { Title, Text } = Typography;

const SCHEDULE_OPTIONS = [
  { value: 'daily', label: '每日 (24h)' },
  { value: 'weekly', label: '每周 (7d)' },
  { value: 'manual', label: '仅手动' },
];

const emptyForm = {
  name: '',
  target_channel_id: null,
  categories: [],
  top_n: 0,
  schedule: 'manual',
  enabled: true,
};

const OpenRouterSync = () => {
  const [jobs, setJobs] = useState([]);
  const [categories, setCategories] = useState([]);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(false);
  const [editing, setEditing] = useState(null); // null = closed; {} = create; job = edit
  const [formValues, setFormValues] = useState(emptyForm);
  const [previewing, setPreviewing] = useState(null); // {jobId, items[]}
  const [running, setRunning] = useState({});

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const [jobRes, catRes, chanRes] = await Promise.all([
        API.get('/api/openrouter-sync/jobs'),
        API.get('/api/openrouter-sync/categories'),
        API.get('/api/channel/?p=0&page_size=200'),
      ]);
      if (jobRes.data?.success) setJobs(jobRes.data.data || []);
      if (catRes.data?.success) setCategories(catRes.data.data || []);
      if (chanRes.data?.success) {
        const list = chanRes.data.data?.items || chanRes.data.data || [];
        // OpenRouter ChannelType is 20
        setChannels(list.filter((c) => c.type === 20));
      }
    } catch (e) {
      Notification.error({ title: i18next.t('加载失败'), content: String(e) });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  const openCreate = () => {
    setFormValues(emptyForm);
    setEditing({});
  };

  const openEdit = (job) => {
    setFormValues({
      name: job.name,
      target_channel_id: job.target_channel_id,
      categories: parseCategories(job.categories),
      top_n: job.top_n,
      schedule: job.schedule,
      enabled: job.enabled,
    });
    setEditing(job);
  };

  const submit = async () => {
    const isCreate = !editing.id;
    if (
      !formValues.name ||
      !formValues.target_channel_id ||
      !formValues.categories?.length
    ) {
      Notification.warning({
        title: i18next.t('校验失败'),
        content: i18next.t('名称、目标渠道、分类必填'),
      });
      return;
    }
    try {
      const res = isCreate
        ? await API.post('/api/openrouter-sync/jobs', formValues)
        : await API.put(`/api/openrouter-sync/jobs/${editing.id}`, formValues);
      if (res.data?.success) {
        Notification.success({ title: i18next.t('已保存') });
        setEditing(null);
        reload();
      } else {
        Notification.error({
          title: '保存失败',
          content: res.data?.message || i18next.t('未知错误'),
        });
      }
    } catch (e) {
      Notification.error({ title: i18next.t('保存失败'), content: String(e) });
    }
  };

  const remove = async (job) => {
    Modal.confirm({
      title: `删除任务 "${job.name}"？`,
      content: i18next.t(
        '已导入的模型不会被立刻清理；下次同步时会按其他规则重新计算。',
      ),
      onOk: async () => {
        try {
          const res = await API.delete(`/api/openrouter-sync/jobs/${job.id}`);
          if (res.data?.success) {
            Notification.success({ title: i18next.t('已删除') });
            reload();
          }
        } catch (e) {
          Notification.error({
            title: i18next.t('删除失败'),
            content: String(e),
          });
        }
      },
    });
  };

  const runNow = async (job, force = false) => {
    setRunning((r) => ({ ...r, [job.id]: true }));
    try {
      const res = await API.post(
        `/api/openrouter-sync/jobs/${job.id}/run${force ? '?force=true' : ''}`,
      );
      if (res.data?.success) {
        const d = res.data.data;
        if (d?.skipped) {
          Notification.info({
            title: i18next.t('已跳过'),
            content: d.skip_reason,
          });
        } else if (d?.circuit_breaker_on) {
          Modal.warning({
            title: i18next.t('熔断保护已触发'),
            content: `本次抓到的模型数量过少，已中止写入。如确认上游变化，请用强制执行重置基准。`,
          });
        } else {
          Notification.success({
            title: i18next.t('执行完成'),
            content: `新增 ${d?.added?.length || 0} 移除 ${d?.removed?.length || 0}`,
          });
        }
        reload();
      } else {
        Notification.error({
          title: i18next.t('执行失败'),
          content: res.data?.message,
        });
      }
    } catch (e) {
      Notification.error({ title: i18next.t('执行失败'), content: String(e) });
    } finally {
      setRunning((r) => ({ ...r, [job.id]: false }));
    }
  };

  const preview = async (job) => {
    try {
      const res = await API.get(`/api/openrouter-sync/jobs/${job.id}/preview`);
      if (res.data?.success) {
        setPreviewing({
          jobId: job.id,
          name: job.name,
          items: res.data.data || [],
        });
      } else {
        Notification.error({
          title: i18next.t('预览失败'),
          content: res.data?.message,
        });
      }
    } catch (e) {
      Notification.error({ title: i18next.t('预览失败'), content: String(e) });
    }
  };

  const channelOptions = channels.map((c) => ({
    value: c.id,
    label: `${c.name} (#${c.id})`,
  }));
  const categoryOptions = categories.map((c) => ({
    value: c.key,
    label: c.label,
  }));

  const columns = [
    { title: '名称', dataIndex: 'name' },
    {
      title: '目标渠道',
      dataIndex: 'target_channel_id',
      render: (id) => {
        const c = channels.find((x) => x.id === id);
        return c ? `${c.name} (#${id})` : `#${id}`;
      },
    },
    {
      title: '分类',
      dataIndex: 'categories',
      render: (raw) =>
        parseCategories(raw).map((k) => {
          const c = categories.find((x) => x.key === k);
          return (
            <Tag key={k} color='blue' style={{ marginRight: 4 }}>
              {c?.label || k}
            </Tag>
          );
        }),
    },
    {
      title: 'Top N',
      dataIndex: 'top_n',
      render: (n) => (n > 0 ? n : '不限'),
    },
    { title: '调度', dataIndex: 'schedule' },
    {
      title: '启用',
      dataIndex: 'enabled',
      render: (v) => (v ? <Tag color='green'>是</Tag> : <Tag>否</Tag>),
    },
    {
      title: '上次运行',
      dataIndex: 'last_run_at',
      render: (t, row) => (
        <div>
          <div>{t ? new Date(t).toLocaleString() : '—'}</div>
          {row.last_error ? (
            <Text type='danger' size='small' ellipsis={{ showTooltip: true }}>
              {row.last_error}
            </Text>
          ) : null}
        </div>
      ),
    },
    {
      title: '操作',
      render: (_, row) => (
        <Space>
          <Button size='small' onClick={() => preview(row)}>
            预览
          </Button>
          <Button
            size='small'
            theme='solid'
            loading={!!running[row.id]}
            onClick={() => runNow(row, false)}
          >
            立即执行
          </Button>
          <Button size='small' onClick={() => runNow(row, true)} type='warning'>
            强制执行
          </Button>
          <Button size='small' onClick={() => openEdit(row)}>
            编辑
          </Button>
          <Button size='small' type='danger' onClick={() => remove(row)}>
            删除
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div className='px-2'>
      <ApiPoolCard />
      <Card style={{ marginBottom: 12 }}>
        <Title heading={4}>OpenRouter 免费模型同步</Title>
        <Text type='tertiary'>
          定期/手动从 OpenRouter
          拉取免费模型，按分类与排名导入到目标渠道。同步引擎是单一全局引擎：
          多个任务可指向同一渠道，互不踩踏；管理员手工添加的模型不会被清理。
        </Text>
        <div style={{ marginTop: 12 }}>
          <Space>
            <Button theme='solid' onClick={openCreate}>
              新建任务
            </Button>
            <Button onClick={reload}>刷新</Button>
          </Space>
        </div>
      </Card>

      <Card>
        <Table
          columns={columns}
          dataSource={jobs}
          loading={loading}
          rowKey='id'
          pagination={false}
        />
      </Card>

      <Modal
        title={
          editing && editing.id ? `编辑任务 #${editing.id}` : '新建同步任务'
        }
        visible={!!editing}
        onCancel={() => setEditing(null)}
        onOk={submit}
        okText='保存'
        cancelText='取消'
        width={560}
      >
        <Form
          initValues={formValues}
          onValueChange={(v) => setFormValues({ ...formValues, ...v })}
        >
          <Form.Input
            field='name'
            label='任务名称'
            placeholder='例：每日免费推理 Top10'
            rules={[{ required: true }]}
          />
          <Form.Select
            field='target_channel_id'
            label='目标渠道 (OpenRouter)'
            optionList={channelOptions}
            placeholder='选择 OpenRouter 渠道'
            rules={[{ required: true }]}
          />
          <Form.Select
            field='categories'
            label='分类（多选）'
            multiple
            optionList={categoryOptions}
            placeholder='选择要导入的模型分类'
            rules={[{ required: true }]}
          />
          <Form.InputNumber
            field='top_n'
            label='Top N（0 = 不限）'
            min={0}
            max={1000}
          />
          <Form.Select
            field='schedule'
            label='调度'
            optionList={SCHEDULE_OPTIONS}
          />
          <Form.Switch field='enabled' label='启用' />
        </Form>
      </Modal>

      <Modal
        title={`预览：${previewing?.name || ''}`}
        visible={!!previewing}
        onCancel={() => setPreviewing(null)}
        footer={null}
        width={640}
      >
        <Text type='tertiary'>
          按当前任务规则筛选 + 排名后将导入的模型（不会写入数据库）：
        </Text>
        <Table
          columns={[
            { title: '模型 ID', dataIndex: 'id' },
            { title: '名称', dataIndex: 'name' },
            {
              title: 'Created',
              dataIndex: 'created',
              render: (t) =>
                t ? new Date(t * 1000).toLocaleDateString() : '—',
            },
          ]}
          dataSource={previewing?.items || []}
          rowKey='id'
          pagination={false}
          size='small'
        />
      </Modal>
    </div>
  );
};

function parseCategories(raw) {
  if (!raw) return [];
  if (Array.isArray(raw)) return raw;
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
}

export default OpenRouterSync;
