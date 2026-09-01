1.  api-keys 配置用量限制以及可使用模型控制。
    1.  调用 models.dev 花费（已实现），自动做一个总花费控制，可设置日/周/月
    2.  总花费可以控制在provider level，即不同provider可设置不同花费，也可以添加总额。
        1.  不设置等于不限制
        2.  user 登录后 overview 可以看到被设置的总花费限制
            1.  user 登录已经实现
2.  api-keys 重新生成和修改功能，可以调用cpa原生的功能