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
import React, { useEffect, useState, useRef } from 'react';
import {
  Button,
  Form,
  Row,
  Col,
  Typography,
  Banner,
  TagInput,
  Spin,
  Card,
  Radio,
} from '@douyinfe/semi-ui';
const { Text } = Typography;
import {
  API,
  removeTrailingSlash,
  showError,
  showSuccess,
  toBoolean,
} from '../../helpers';
import { useTranslation } from 'react-i18next';
import SettingsSensitiveWords from '../../pages/Setting/Operation/SettingsSensitiveWords';

const SecuritySettingPage = () => {
  const { t } = useTranslation();
  let [inputs, setInputs] = useState({
    WorkerUrl: '',
    WorkerValidKey: '',
    WorkerAllowHttpImageRequestEnabled: '',
    // SSRF
    'fetch_setting.enable_ssrf_protection': true,
    'fetch_setting.allow_private_ip': '',
    'fetch_setting.domain_filter_mode': false,
    'fetch_setting.ip_filter_mode': false,
    'fetch_setting.domain_list': [],
    'fetch_setting.ip_list': [],
    'fetch_setting.allowed_ports': [],
    'fetch_setting.apply_ip_filter_for_domain': false,
    // Sensitive words
    CheckSensitiveEnabled: false,
    CheckSensitiveOnPromptEnabled: false,
    SensitiveWords: '',
  });

  const [originInputs, setOriginInputs] = useState({});
  const [loading, setLoading] = useState(false);
  const [isLoaded, setIsLoaded] = useState(false);
  const formApiRef = useRef(null);
  const [domainFilterMode, setDomainFilterMode] = useState(true);
  const [ipFilterMode, setIpFilterMode] = useState(true);
  const [domainList, setDomainList] = useState([]);
  const [ipList, setIpList] = useState([]);
  const [allowedPorts, setAllowedPorts] = useState([]);

  const getOptions = async () => {
    setLoading(true);
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        switch (item.key) {
          case 'fetch_setting.allow_private_ip':
          case 'fetch_setting.enable_ssrf_protection':
          case 'fetch_setting.domain_filter_mode':
          case 'fetch_setting.ip_filter_mode':
          case 'fetch_setting.apply_ip_filter_for_domain':
            item.value = toBoolean(item.value);
            break;
          case 'fetch_setting.domain_list':
            try {
              const domains = item.value ? JSON.parse(item.value) : [];
              setDomainList(Array.isArray(domains) ? domains : []);
            } catch (e) {
              setDomainList([]);
            }
            break;
          case 'fetch_setting.ip_list':
            try {
              const ips = item.value ? JSON.parse(item.value) : [];
              setIpList(Array.isArray(ips) ? ips : []);
            } catch (e) {
              setIpList([]);
            }
            break;
          case 'fetch_setting.allowed_ports':
            try {
              const ports = item.value ? JSON.parse(item.value) : [];
              setAllowedPorts(Array.isArray(ports) ? ports : []);
            } catch (e) {
              setAllowedPorts(['80', '443', '8080', '8443']);
            }
            break;
          case 'WorkerAllowHttpImageRequestEnabled':
          case 'CheckSensitiveEnabled':
          case 'CheckSensitiveOnPromptEnabled':
            item.value = toBoolean(item.value);
            break;
          default:
            break;
        }
        newInputs[item.key] = item.value;
      });
      setInputs(newInputs);
      setOriginInputs(newInputs);
      if (
        typeof newInputs['fetch_setting.domain_filter_mode'] !== 'undefined'
      ) {
        setDomainFilterMode(!!newInputs['fetch_setting.domain_filter_mode']);
      }
      if (typeof newInputs['fetch_setting.ip_filter_mode'] !== 'undefined') {
        setIpFilterMode(!!newInputs['fetch_setting.ip_filter_mode']);
      }
      if (formApiRef.current) {
        formApiRef.current.setValues(newInputs);
      }
      setIsLoaded(true);
    } else {
      showError(message);
    }
    setLoading(false);
  };

  useEffect(() => {
    getOptions();
  }, []);

  const updateOptions = async (options) => {
    setLoading(true);
    try {
      const checkboxOptions = options.filter((opt) =>
        opt.key.toLowerCase().endsWith('enabled'),
      );
      const otherOptions = options.filter(
        (opt) => !opt.key.toLowerCase().endsWith('enabled'),
      );

      for (const opt of checkboxOptions) {
        const res = await API.put('/api/option/', {
          key: opt.key,
          value: opt.value.toString(),
        });
        if (!res.data.success) {
          showError(res.data.message);
          return;
        }
      }

      if (otherOptions.length > 0) {
        const requestQueue = otherOptions.map((opt) =>
          API.put('/api/option/', {
            key: opt.key,
            value:
              typeof opt.value === 'boolean' ? opt.value.toString() : opt.value,
          }),
        );
        const results = await Promise.all(requestQueue);
        const errorResults = results.filter((res) => !res.data.success);
        errorResults.forEach((res) => {
          showError(res.data.message);
        });
      }

      showSuccess(t('更新成功'));
      const newInputs = { ...inputs };
      options.forEach((opt) => {
        newInputs[opt.key] = opt.value;
      });
      setInputs(newInputs);
    } catch (error) {
      showError(t('更新失败'));
    }
    setLoading(false);
  };

  const handleFormChange = (values) => {
    setInputs(values);
  };

  const handleCheckboxChange = async (optionKey, event) => {
    const value = event.target.checked;
    await updateOptions([{ key: optionKey, value }]);
  };

  const submitWorker = async () => {
    let WorkerUrl = removeTrailingSlash(inputs.WorkerUrl);
    const options = [
      { key: 'WorkerUrl', value: WorkerUrl },
      {
        key: 'WorkerAllowHttpImageRequestEnabled',
        value: inputs.WorkerAllowHttpImageRequestEnabled ? 'true' : 'false',
      },
    ];
    if (inputs.WorkerValidKey !== '' || WorkerUrl === '') {
      options.push({ key: 'WorkerValidKey', value: inputs.WorkerValidKey });
    }
    await updateOptions(options);
  };

  const submitSSRF = async () => {
    const options = [];
    options.push({
      key: 'fetch_setting.domain_filter_mode',
      value: domainFilterMode,
    });
    if (Array.isArray(domainList)) {
      options.push({
        key: 'fetch_setting.domain_list',
        value: JSON.stringify(domainList),
      });
    }
    options.push({
      key: 'fetch_setting.ip_filter_mode',
      value: ipFilterMode,
    });
    if (Array.isArray(ipList)) {
      options.push({
        key: 'fetch_setting.ip_list',
        value: JSON.stringify(ipList),
      });
    }
    if (Array.isArray(allowedPorts)) {
      options.push({
        key: 'fetch_setting.allowed_ports',
        value: JSON.stringify(allowedPorts),
      });
    }
    if (options.length > 0) {
      await updateOptions(options);
    }
  };

  // Refresh callback for SettingsSensitiveWords child
  const refresh = async () => {
    await getOptions();
  };

  return (
    <div>
      {isLoaded ? (
        <Form
          initValues={inputs}
          onValueChange={handleFormChange}
          getFormApi={(api) => (formApiRef.current = api)}
        >
          {({ formState, values, formApi }) => (
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: '10px',
                marginTop: '10px',
              }}
            >
              <Card>
                <Form.Section text={t('代理设置')}>
                  <Banner
                    type='info'
                    description={t(
                      '此代理仅用于图片请求转发，Webhook通知发送等，AI API请求仍然由服务器直接发出，可在渠道设置中单独配置代理',
                    )}
                    style={{ marginBottom: 20, marginTop: 16 }}
                  />
                  <Text>
                    {t('仅支持')}{' '}
                    <a
                      href='https://github.com/Calcium-Ion/lurus-api-worker'
                      target='_blank'
                      rel='noreferrer'
                    >
                      lurus-api-worker
                    </a>{' '}
                    {t('或其兼容lurus-api-worker格式的其他版本')}
                  </Text>
                  <Row
                    gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
                  >
                    <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                      <Form.Input
                        field='WorkerUrl'
                        label={t('Worker地址')}
                        placeholder={t(
                          '例如：https://workername.yourdomain.workers.dev',
                        )}
                      />
                    </Col>
                    <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                      <Form.Input
                        field='WorkerValidKey'
                        label={t('Worker密钥')}
                        placeholder={t('敏感信息不会发送到前端显示')}
                        type='password'
                      />
                    </Col>
                  </Row>
                  <Form.Checkbox
                    field='WorkerAllowHttpImageRequestEnabled'
                    noLabel
                  >
                    {t('允许 HTTP 协议图片请求（适用于自部署代理）')}
                  </Form.Checkbox>
                  <Button onClick={submitWorker}>{t('更新Worker设置')}</Button>
                </Form.Section>
              </Card>

              <Card>
                <Form.Section text={t('SSRF防护设置')}>
                  <Text extraText={t('SSRF防护详细说明')}>
                    {t('配置服务器端请求伪造(SSRF)防护，用于保护内网资源安全')}
                  </Text>
                  <Row
                    gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
                  >
                    <Col xs={24} sm={24} md={24} lg={24} xl={24}>
                      <Form.Checkbox
                        field='fetch_setting.enable_ssrf_protection'
                        noLabel
                        extraText={t('SSRF防护开关详细说明')}
                        onChange={(e) =>
                          handleCheckboxChange(
                            'fetch_setting.enable_ssrf_protection',
                            e,
                          )
                        }
                      >
                        {t('启用SSRF防护（推荐开启以保护服务器安全）')}
                      </Form.Checkbox>
                    </Col>
                  </Row>

                  <Row
                    gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
                    style={{ marginTop: 16 }}
                  >
                    <Col xs={24} sm={24} md={24} lg={24} xl={24}>
                      <Form.Checkbox
                        field='fetch_setting.allow_private_ip'
                        noLabel
                        extraText={t('私有IP访问详细说明')}
                        onChange={(e) =>
                          handleCheckboxChange(
                            'fetch_setting.allow_private_ip',
                            e,
                          )
                        }
                      >
                        {t(
                          '允许访问私有IP地址（127.0.0.1、192.168.x.x等内网地址）',
                        )}
                      </Form.Checkbox>
                    </Col>
                  </Row>

                  <Row
                    gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
                    style={{ marginTop: 16 }}
                  >
                    <Col xs={24} sm={24} md={24} lg={24} xl={24}>
                      <Form.Checkbox
                        field='fetch_setting.apply_ip_filter_for_domain'
                        noLabel
                        extraText={t('域名IP过滤详细说明')}
                        onChange={(e) =>
                          handleCheckboxChange(
                            'fetch_setting.apply_ip_filter_for_domain',
                            e,
                          )
                        }
                        style={{ marginBottom: 8 }}
                      >
                        {t('对域名启用 IP 过滤（实验性）')}
                      </Form.Checkbox>
                      <Text strong>
                        {t(domainFilterMode ? '域名白名单' : '域名黑名单')}
                      </Text>
                      <Text
                        type='secondary'
                        style={{ display: 'block', marginBottom: 8 }}
                      >
                        {t(
                          '支持通配符格式，如：example.com, *.api.example.com',
                        )}
                      </Text>
                      <Radio.Group
                        type='button'
                        value={domainFilterMode ? 'whitelist' : 'blacklist'}
                        onChange={(val) => {
                          const selected =
                            val && val.target ? val.target.value : val;
                          const isWhitelist = selected === 'whitelist';
                          setDomainFilterMode(isWhitelist);
                          setInputs((prev) => ({
                            ...prev,
                            'fetch_setting.domain_filter_mode': isWhitelist,
                          }));
                        }}
                        style={{ marginBottom: 8 }}
                      >
                        <Radio value='whitelist'>{t('白名单')}</Radio>
                        <Radio value='blacklist'>{t('黑名单')}</Radio>
                      </Radio.Group>
                      <TagInput
                        value={domainList}
                        onChange={(value) => {
                          setDomainList(value);
                          setInputs((prev) => ({
                            ...prev,
                            'fetch_setting.domain_list': value,
                          }));
                        }}
                        placeholder={t('输入域名后回车，如：example.com')}
                        style={{ width: '100%' }}
                      />
                    </Col>
                  </Row>

                  <Row
                    gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
                    style={{ marginTop: 16 }}
                  >
                    <Col xs={24} sm={24} md={24} lg={24} xl={24}>
                      <Text strong>
                        {t(ipFilterMode ? 'IP白名单' : 'IP黑名单')}
                      </Text>
                      <Text
                        type='secondary'
                        style={{ display: 'block', marginBottom: 8 }}
                      >
                        {t('支持CIDR格式，如：8.8.8.8, 192.168.1.0/24')}
                      </Text>
                      <Radio.Group
                        type='button'
                        value={ipFilterMode ? 'whitelist' : 'blacklist'}
                        onChange={(val) => {
                          const selected =
                            val && val.target ? val.target.value : val;
                          const isWhitelist = selected === 'whitelist';
                          setIpFilterMode(isWhitelist);
                          setInputs((prev) => ({
                            ...prev,
                            'fetch_setting.ip_filter_mode': isWhitelist,
                          }));
                        }}
                        style={{ marginBottom: 8 }}
                      >
                        <Radio value='whitelist'>{t('白名单')}</Radio>
                        <Radio value='blacklist'>{t('黑名单')}</Radio>
                      </Radio.Group>
                      <TagInput
                        value={ipList}
                        onChange={(value) => {
                          setIpList(value);
                          setInputs((prev) => ({
                            ...prev,
                            'fetch_setting.ip_list': value,
                          }));
                        }}
                        placeholder={t('输入IP地址后回车，如：8.8.8.8')}
                        style={{ width: '100%' }}
                      />
                    </Col>
                  </Row>

                  <Row
                    gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
                    style={{ marginTop: 16 }}
                  >
                    <Col xs={24} sm={24} md={24} lg={24} xl={24}>
                      <Text strong>{t('允许的端口')}</Text>
                      <Text
                        type='secondary'
                        style={{ display: 'block', marginBottom: 8 }}
                      >
                        {t('支持单个端口和端口范围，如：80, 443, 8000-8999')}
                      </Text>
                      <TagInput
                        value={allowedPorts}
                        onChange={(value) => {
                          setAllowedPorts(value);
                          setInputs((prev) => ({
                            ...prev,
                            'fetch_setting.allowed_ports': value,
                          }));
                        }}
                        placeholder={t('输入端口后回车，如：80 或 8000-8999')}
                        style={{ width: '100%' }}
                      />
                      <Text
                        type='secondary'
                        style={{ display: 'block', marginBottom: 8 }}
                      >
                        {t('端口配置详细说明')}
                      </Text>
                    </Col>
                  </Row>

                  <Button onClick={submitSSRF} style={{ marginTop: 16 }}>
                    {t('更新SSRF防护设置')}
                  </Button>
                </Form.Section>
              </Card>

              {/* Sensitive words filter - reuse existing child component */}
              <Card>
                <SettingsSensitiveWords options={inputs} refresh={refresh} />
              </Card>
            </div>
          )}
        </Form>
      ) : (
        <div
          style={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            height: '100vh',
          }}
        >
          <Spin size='large' />
        </div>
      )}
    </div>
  );
};

export default SecuritySettingPage;
