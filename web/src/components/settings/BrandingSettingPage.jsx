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
import React, { useContext, useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Col,
  Form,
  Row,
  Modal,
  Space,
  Card,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess, timestamp2string } from '../../helpers';
import { marked } from 'marked';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import Text from '@douyinfe/semi-ui/lib/es/typography/text';

const BrandingSettingPage = () => {
  const { t } = useTranslation();
  const [inputs, setInputs] = useState({
    SystemName: '',
    Logo: '',
    Footer: '',
    About: '',
    HomePageContent: '',
  });
  const [loading, setLoading] = useState(false);
  const [showUpdateModal, setShowUpdateModal] = useState(false);
  const [statusState] = useContext(StatusContext);
  const [updateData, setUpdateData] = useState({
    tag_name: '',
    content: '',
  });

  const [loadingInput, setLoadingInput] = useState({
    SystemName: false,
    Logo: false,
    HomePageContent: false,
    About: false,
    Footer: false,
    CheckUpdate: false,
  });

  const formApiRef = useRef();

  const updateOption = async (key, value) => {
    setLoading(true);
    const res = await API.put('/api/option/', { key, value });
    const { success, message } = res.data;
    if (success) {
      setInputs((prev) => ({ ...prev, [key]: value }));
    } else {
      showError(message);
    }
    setLoading(false);
  };

  const handleInputChange = async (value, e) => {
    const name = e.target.id;
    setInputs((prev) => ({ ...prev, [name]: value }));
  };

  const submitSystemName = async () => {
    try {
      setLoadingInput((s) => ({ ...s, SystemName: true }));
      await updateOption('SystemName', inputs.SystemName);
      showSuccess(t('系统名称已更新'));
    } catch (error) {
      showError(t('系统名称更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, SystemName: false }));
    }
  };

  const submitLogo = async () => {
    try {
      setLoadingInput((s) => ({ ...s, Logo: true }));
      await updateOption('Logo', inputs.Logo);
      showSuccess(t('Logo 已更新'));
    } catch (error) {
      showError(t('Logo 更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, Logo: false }));
    }
  };

  const submitHomePageContent = async () => {
    try {
      setLoadingInput((s) => ({ ...s, HomePageContent: true }));
      await updateOption('HomePageContent', inputs.HomePageContent);
      showSuccess(t('首页内容已更新'));
    } catch (error) {
      showError(t('首页内容更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, HomePageContent: false }));
    }
  };

  const submitAbout = async () => {
    try {
      setLoadingInput((s) => ({ ...s, About: true }));
      await updateOption('About', inputs.About);
      showSuccess(t('关于内容已更新'));
    } catch (error) {
      showError(t('关于内容更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, About: false }));
    }
  };

  const submitFooter = async () => {
    try {
      setLoadingInput((s) => ({ ...s, Footer: true }));
      await updateOption('Footer', inputs.Footer);
      showSuccess(t('页脚内容已更新'));
    } catch (error) {
      showError(t('页脚内容更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, Footer: false }));
    }
  };

  const checkUpdate = async () => {
    try {
      setLoadingInput((s) => ({ ...s, CheckUpdate: true }));
      const res = await fetch(
        'https://api.github.com/repos/Calcium-Ion/lurus-api/releases/latest',
        {
          headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
            'User-Agent': 'lurus-api-update-checker',
          },
        },
      ).then((response) => response.json());

      const { tag_name, body } = res;
      if (tag_name === statusState?.status?.version) {
        showSuccess(`已是最新版本：${tag_name}`);
      } else {
        setUpdateData({
          tag_name: tag_name,
          content: marked.parse(body),
        });
        setShowUpdateModal(true);
      }
    } catch (error) {
      showError(t('检查更新失败，请稍后再试'));
    } finally {
      setLoadingInput((s) => ({ ...s, CheckUpdate: false }));
    }
  };

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      const newInputs = {};
      data.forEach((item) => {
        if (item.key in inputs) {
          newInputs[item.key] = item.value;
        }
      });
      setInputs(newInputs);
      if (formApiRef.current) {
        formApiRef.current.setValues(newInputs);
      }
    } else {
      showError(message);
    }
  };

  useEffect(() => {
    getOptions();
  }, []);

  const openGitHubRelease = () => {
    window.open(
      `https://github.com/Calcium-Ion/lurus-api/releases/tag/${updateData.tag_name}`,
      '_blank',
    );
  };

  const getStartTimeString = () => {
    const timestamp = statusState?.status?.start_time;
    return statusState.status ? timestamp2string(timestamp) : '';
  };

  return (
    <Row>
      <Col
        span={24}
        style={{
          marginTop: '10px',
          display: 'flex',
          flexDirection: 'column',
          gap: '10px',
        }}
      >
        {/* System info */}
        <Form>
          <Card>
            <Form.Section text={t('系统信息')}>
              <Row>
                <Col span={16}>
                  <Space>
                    <Text>
                      {t('当前版本')}：
                      {statusState?.status?.version || t('未知')}
                    </Text>
                    <Button
                      type='primary'
                      onClick={checkUpdate}
                      loading={loadingInput['CheckUpdate']}
                    >
                      {t('检查更新')}
                    </Button>
                  </Space>
                </Col>
              </Row>
              <Row>
                <Col span={16}>
                  <Text>
                    {t('启动时间')}：{getStartTimeString()}
                  </Text>
                </Col>
              </Row>
            </Form.Section>
          </Card>
        </Form>

        {/* Branding / personalization */}
        <Form values={inputs} getFormApi={(api) => (formApiRef.current = api)}>
          <Card>
            <Form.Section text={t('个性化设置')}>
              <Form.Input
                label={t('系统名称')}
                placeholder={t('在此输入系统名称')}
                field='SystemName'
                onChange={handleInputChange}
              />
              <Button
                onClick={submitSystemName}
                loading={loadingInput['SystemName']}
              >
                {t('设置系统名称')}
              </Button>
              <Form.Input
                label={t('Logo 图片地址')}
                placeholder={t('在此输入 Logo 图片地址')}
                field='Logo'
                onChange={handleInputChange}
              />
              <Button onClick={submitLogo} loading={loadingInput['Logo']}>
                {t('设置 Logo')}
              </Button>
              <Form.TextArea
                label={t('首页内容')}
                placeholder={t(
                  '在此输入首页内容，支持 Markdown & HTML 代码，设置后首页的状态信息将不再显示。如果输入的是一个链接，则会使用该链接作为 iframe 的 src 属性，这允许你设置任意网页作为首页',
                )}
                field='HomePageContent'
                onChange={handleInputChange}
                style={{ fontFamily: 'JetBrains Mono, Consolas' }}
                autosize={{ minRows: 6, maxRows: 12 }}
              />
              <Button
                onClick={submitHomePageContent}
                loading={loadingInput['HomePageContent']}
              >
                {t('设置首页内容')}
              </Button>
              <Form.TextArea
                label={t('关于')}
                placeholder={t(
                  '在此输入新的关于内容，支持 Markdown & HTML 代码。如果输入的是一个链接，则会使用该链接作为 iframe 的 src 属性，这允许你设置任意网页作为关于页面',
                )}
                field='About'
                onChange={handleInputChange}
                style={{ fontFamily: 'JetBrains Mono, Consolas' }}
                autosize={{ minRows: 6, maxRows: 12 }}
              />
              <Button onClick={submitAbout} loading={loadingInput['About']}>
                {t('设置关于')}
              </Button>
              <Banner
                fullMode={false}
                type='info'
                description={t(
                  '移除 One API 的版权标识必须首先获得授权，项目维护需要花费大量精力，如果本项目对你有意义，请主动支持本项目',
                )}
                closeIcon={null}
                style={{ marginTop: 15 }}
              />
              <Form.Input
                label={t('页脚')}
                placeholder={t(
                  '在此输入新的页脚，留空则使用默认页脚，支持 HTML 代码',
                )}
                field='Footer'
                onChange={handleInputChange}
              />
              <Button onClick={submitFooter} loading={loadingInput['Footer']}>
                {t('设置页脚')}
              </Button>
            </Form.Section>
          </Card>
        </Form>
      </Col>

      <Modal
        title={t('新版本') + '：' + updateData.tag_name}
        visible={showUpdateModal}
        onCancel={() => setShowUpdateModal(false)}
        footer={[
          <Button
            key='details'
            type='primary'
            onClick={() => {
              setShowUpdateModal(false);
              openGitHubRelease();
            }}
          >
            {t('详情')}
          </Button>,
        ]}
      >
        <div dangerouslySetInnerHTML={{ __html: updateData.content }}></div>
      </Modal>
    </Row>
  );
};

export default BrandingSettingPage;
