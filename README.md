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
1.认证user.token字段修改 使用被测试的openstack 用户，创建对应的环境变量文件 以230为例子  注意这里使用凭据需要注意对应的权限。

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

替换ecns_v1_plan.yaml中的
token: "gAAAAABkp3jlbD7nZfmgREWH-jX1200cZIEY7-SPiaWfKbv_F8HIDca5fycrHo9WER_CesphdPh_yYgn8tn7OMsSmJx663FK6DMBKoQpRfsvdShiNwzVG09ADMCXmC3SmFhsPdRCdsGuqwHl5gxmL7PMM6BuPBxD0Qkd3cKGDnJMHbYzJ-_0HT0" 字段

2.修改集群其他信息 例如cluster_name等 注意目前还不存在验重逻辑 因此cluster_name不能和已应用plan cr一致。
3.应用cr
kubectl apply -f ecns_v1_plan.yaml
```
- 3 添加本地认证host
```
172.35.0.2 keystone.openstack.svc.cluster.local
172.35.0.2 nova.openstack.svc.cluster.local
172.35.0.2 neutron.openstack.svc.cluster.local
172.35.0.2 octavia.octavia.svc.cluster.local
```
-  4.程序调试
```
go run cmd/main.go
```
- 5.查看虚拟机创建日志
```
1.控制器日志
kubectl  -n test-capo logs capo-controller-manager-684ff6fd7f-d4kg5
2.资源message(以test1master0-vfrxn这台虚拟机为例)
kubectl describe openstackmachine -n test-capo test1master0-vfrxn
3.事件
kubectl get event -n test-capo
```

[TOC]

## 镜像打包

### 总体流程如下

编写文件 -> 调用打包命令打包镜像 -> 测试镜像

### 打包镜像

ES将Kolla封装成Cube项目,本项目基于Cube进行镜像打包
镜像打包命令如下：

X86:
```shell
cd /data/projects/cube 

sudo tools/build.py <project>/<image> --skip-parents --type source --base escloud-linux --registry hub.easystack.io --namespace production --tag 6.0.1-beta.176
```

ARM:
```shell
cd /data/projects/cube

sudo tools/build.py <project>/<image>  --skip-parents --type source --base escloud-linux --registry hub.easystack.io --namespace arm64v8 --tag 6.0.1-beta.176
```

**Tips**：

1. 该命令需要编写完成镜像文件以后才可以调用。

2. 自测环境如果不能使用密码登陆，需要向相应具备权限的人请求帮忙添加公钥。

3. 该命令只构建所指定的镜像，如果发现并非自己指定的镜像却被打包了，立即暂停。

4. project/image实际上对应cube/docker目录下的一级目录和二级目录名称；比如编写了cube/docker/launcher/launcher-captain/...(这里面可能不仅仅包含Dockerfile.j2还可能有你编写的其他脚本文件)的话你的调用命令应该为：

```shell
sudo tools/build.py launcher/launcher-captain  --skip-parents --type source --base escloud-linux --registry hub.easystack.io --namespace arm64v8 --tag 6.0.1-beta.176
```

还需要注意的事是，如果你的镜像名称为`<project name>-base`，以`launcher\launcher-base`为例，调用命令如下：

```shell
sudo tools/build.py launcher-base  --skip-parents --type source --base escloud-linux --registry hub.easystack.io --namespace arm64v8 --tag 6.0.1-beta.176
```

5. 文件编写须知：Cube没有人负责其更新，在使用或者编写之前需要将Cube更新至最新的版本。

### 编写文件

编写镜像文件流程如下：

1. 在cube/docker目录下对相应的project/image进行修改
    - 在该目录下编写Dockerfile.j2模板文件，会根据该模板文件生成Dockerfile。比如本项目的镜像分别存放在`cube/docker/launcher/launcher-base`；`cube/docker/launcher/launcher-captain`。
2. 安装系统的包，可以先指定需要安装的包的列表，随后调用`macros.install_packages()`安装系统包。
3. 动态获取代码的需求
    - 修改`kolla/common/config.py`，在`_PROFILE_OPTS`中的aux中添加相应的镜像名称,如`launcher-base`，在`SOURCES`中分别配置源和分支等信息（如果是公司的源和分支直接参考其他项目，如果使用了其他源，那就需要参考公司默认的形式，然后硬编码相应情况）。
    - 在Dockerfile.j2中调用`{{macros.curl_archive_codes('launcher/launcher-base', 'launcher-base-archive', 'launcher-base-source')}}`，这一部分要被封装在`
{% if install_type == 'source' %}`就可以动态下载代码。

**Tips**：

1. `kolla/common/config.py`中`aux`和`SOURCES`中的Key对应的是cmd命令行中解析出来的image名称,不是macros.curl_archive_codes中的参数。

### 简单报错提示

1. unmatched代表的是cmd调用的时候使用的镜像名称有误，没配对上。

2. 对于镜像配置过程中需要检测问题的可以在相应位置插入sleep命令，然后进入容器查看。