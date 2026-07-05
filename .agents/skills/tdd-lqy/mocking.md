# 何时模拟

仅模拟**系统边界**：

- 外部API（支付、电子邮件等）
- 数据库（有时 - 更喜欢测试数据库）
- 时间/随机性
- 文件系统（有时）

不要嘲笑：

- 你自己的类/模块
- 内部合作者
- 一切由你掌控

## 可模拟性设计

在系统边界，设计易于模拟的接口：

**1.使用依赖注入**

传递外部依赖项而不是在内部创建它们：
```typescript
// Easy to mock
function processPayment(order, paymentClient) {
  return paymentClient.charge(order.total);
}

// Hard to mock
function processPayment(order) {
  const client = new StripeClient(process.env.STRIPE_KEY);
  return client.charge(order.total);
}
```
**2.与通用获取器相比，更喜欢 SDK 风格的接口**

为每个外部操作创建特定的函数，而不是使用条件逻辑的通用函数：
```typescript
// GOOD: Each function is independently mockable
const api = {
  getUser: (id) => fetch(`/users/${id}`),
  getOrders: (userId) => fetch(`/users/${userId}/orders`),
  createOrder: (data) => fetch('/orders', { method: 'POST', body: data }),
};

// BAD: Mocking requires conditional logic inside the mock
const api = {
  fetch: (endpoint, options) => fetch(endpoint, options),
};
```
SDK 方法意味着：
- 每个模拟返回一个特定的形状
- 测试设置中没有条件逻辑
- 更容易查看测试练习的端点
- 每个端点的类型安全