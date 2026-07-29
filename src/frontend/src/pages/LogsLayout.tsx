import { Outlet, useLocation, useNavigate } from 'react-router-dom'

import { Page, PageHeader } from '@/components/PagePrimitives'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

export default function LogsLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const active = location.pathname.endsWith('/requests') ? 'requests' : 'operations'

  return (
    <Page>
      <PageHeader title="日志" description="审阅系统操作与最近的 API 请求流量。" />
      <Tabs
        value={active}
        onValueChange={(value) => navigate(`/logs/${value}`)}
        className="min-w-0"
      >
        <TabsList variant="line">
          <TabsTrigger value="operations">操作日志</TabsTrigger>
          <TabsTrigger value="requests">请求日志</TabsTrigger>
        </TabsList>
        <TabsContent value={active}>
          <Outlet />
        </TabsContent>
      </Tabs>
    </Page>
  )
}
