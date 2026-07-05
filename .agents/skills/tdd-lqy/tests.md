# 好的和坏的测试

## 良好的测试

**集成式**：通过真实接口进行测试，而不是内部部件的模拟。
```typescript
// GOOD: Tests observable behavior
test("user can checkout with valid cart", async () => {
  const cart = createCart();
  cart.add(product);
  const result = await checkout(cart, paymentMethod);
  expect(result.status).toBe("confirmed");
});
```
特点：

- 测试用户/呼叫者关心的行为
- 仅使用公共 API
- 经受内部重构
- 描述什么，而不是如何
- 每次测试一个逻辑断言

## 糟糕的测试

**实施细节测试**：与内部结构耦合。
```typescript
// BAD: Tests implementation details
test("checkout calls paymentService.process", async () => {
  const mockPayment = jest.mock(paymentService);
  await checkout(cart, payment);
  expect(mockPayment.process).toHaveBeenCalledWith(cart.total);
});
```
危险信号：

- 嘲笑内部合作者
- 测试私有方法
- 断言调用计数/订单
- 在不改变行为的情况下重构时测试中断
- 测试名称描述的是“如何”而不是“是什么”
- 通过外部手段而不是接口进行验证
```typescript
// BAD: Bypasses interface to verify
test("createUser saves to database", async () => {
  await createUser({ name: "Alice" });
  const row = await db.query("SELECT * FROM users WHERE name = ?", ["Alice"]);
  expect(row).toBeDefined();
});

// GOOD: Verifies through interface
test("createUser makes user retrievable", async () => {
  const user = await createUser({ name: "Alice" });
  const retrieved = await getUser(user.id);
  expect(retrieved.name).toBe("Alice");
});
```

