# easystack-eks-op
- EKS Operator for EasyStack
##### 调试方法
- 1.进入plan cr目录
```
cd ./config/samples/
```
- 2.修改自定义资源plan cr
```
主要修改点:
1.认证user.token字段修改 使用被测试的openstack 用户，创建对应的环境变量文件 以230为例子

()[root@busybox-openstack-55f847fb9b-gngnd /]# cat openrc.v3.admin 
#!/bin/sh
for i in `env | grep OS_ | awk -F= '{print $1}'`; do unset $i; done
export OS_IDENTITY_API_VERSION=3
export OS_PROJECT_DOMAIN_NAME="Default"
export OS_PROJECT_NAME="admin"
export OS_USER_DOMAIN_NAME="Default"
export OS_USERNAME="admin"
export OS_PASSWORD="Admin@ES20!8"
export OS_AUTH_URL='http://keystone-api.openstack.svc.cluster.local/v3'
export OS_REGION_NAME="RegionOne"
export OS_ENDPOINT_TYPE='adminURL'
export OS_INTERFACE='adminURL'

# When neutron client returns non-ascii character and stdout is piped or
# redirected, it would raise an encoding error.
export PYTHONIOENCODING=UTF-8

()[root@busybox-openstack-55f847fb9b-gngnd /]# source openrc.v3.admin 
()[root@busybox-openstack-55f847fb9b-gngnd /]# openstack token issue
+------------+-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------+
| Field      | Value                                                                                                                                                                                   |
+------------+-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------+
| expires    | 2023-07-06 08:27:24+00:00                                                                                                                                                               |
| id         | gAAAAABkpiaMpLxO0EKSde6vus1tzCl3da9Nfr6MTjVFMPUfFZzMUa-SoGTLDppElOtFhoWuGh73dGdlJrnw01ANVkjLakV1XvbX7zA-FBwH32qUMKIsGOREVAwmwN3lTloBUYvctPH0QnpAh2AhqKRMRAnx1A2WKykS8XV-oVnSaMhHY2MEo0Y |
| project_id | c4b0683ae6844c569a15bc34e3063256                                                                                                                                                        |
| user_id    | 46e99d741db34395b39c5a76c5af80a4                                                                                                                                                        |
+------------+-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------+

2.修改集群其他信息 例如cluster_name等 注意目前还不存在验重逻辑 因此cluster_name不能和已应用plan cr一致。
3.应用cr
kubectl apply -f ecns_v1_plan.yaml
```
-  3.程序调试
```
go run cmd/main.go
```